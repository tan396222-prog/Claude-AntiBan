import hashlib
from datetime import datetime, timezone
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

sys.dont_write_bytecode = True
source = Path(__file__).with_name("claude-account.py")
spec = importlib.util.spec_from_file_location("account", source)
helper = importlib.util.module_from_spec(spec)
spec.loader.exec_module(helper)
FIRST = "1" * 24
SECOND = "2" * 24


class ClaudeAccountTests(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.home = Path(tmp.name)
        fake = self.home / ".local/bin/claude"
        fake.parent.mkdir(parents=True)
        # Emulate the documented status shape of the official CLI. Its response
        # deliberately includes fields that must never cross the guest boundary.
        fake.write_text("""#!/bin/sh
if [ "$1" = auth ] && [ "$2" = status ]; then
  method=claude.ai; if [ -n "$ANTHROPIC_API_KEY" ]; then method=api_key; fi
  printf '{"configDirectory":"%s","authMethod":"%s","apiProvider":"%s","loggedIn":true,"email":"%s","orgId":"test-org","subscriptionType":"pro","access_token":"must-not-appear"}' "${TEST_CONFIG_RESULT:-${CLAUDE_CONFIG_DIR:-$HOME/.claude}}" "$method" "${TEST_PROVIDER:-firstParty}" "$TEST_EMAIL"
  exit 0
fi
printf '%s\\n' "$@"
""")
        fake.chmod(0o700)
        self.email = "owner@example.test"
        self.subject = hashlib.sha256((self.email + "\ntest-org").encode()).hexdigest()
        self.env = {"HOME": str(self.home), "PATH": os.defpath, "TEST_EMAIL": self.email,
                    "CAGE_CLAUDE_SUBJECT": self.subject, "PYTHONDONTWRITEBYTECODE": "1"}

    def run_helper(self, mode, account, *args, data=None, env=None, check=True):
        return subprocess.run([sys.executable, str(source), mode, account, *args], env=env or self.env,
                              cwd=self.home, input=json.dumps(data) if data else None, text=True,
                              capture_output=True, check=check)

    def test_isolated_cli_snapshot_and_subject_bound_quota(self):
        reset = int(time.time()) + 3600
        window = {"used_percentage": 73.4, "resets_at": reset}
        payload = {"rate_limits": {"five_hour": window, "private": "must-not-appear"},
                   "last_assistant_message": "must-not-appear"}
        self.run_helper("statusline", FIRST, data=payload)
        snapshot = self.run_helper("snapshot", FIRST).stdout
        self.assertNotIn("must-not-appear", snapshot)
        window = json.loads(snapshot)["quota"]["five_hour"]
        self.assertEqual(window["used_percentage"], 73.4)
        self.assertGreater(window["observed_at"], 0)
        self.assertNotIn("quota", json.loads(self.run_helper("snapshot", SECOND).stdout))
        batch = json.loads(self.run_helper("quotas", FIRST, SECOND).stdout)
        self.assertEqual(list(batch), [FIRST])
        self.assertEqual(set(batch[FIRST]), {"identity", "observed_at", "five_hour"})
        self.assertEqual(batch[FIRST]["identity"], self.subject)
        # A later redraw by an older terminal cannot undo a consumed window.
        payload["rate_limits"]["five_hour"] = {**window, "used_percentage": 2}
        self.run_helper("statusline", FIRST, data=payload)
        self.assertEqual(json.loads(self.run_helper("quotas", FIRST).stdout)[FIRST]["five_hour"], window)
        # An interactive /login changes the credential while the old session is alive.
        other_env = {**self.env, "TEST_EMAIL": "another@example.test"}
        payload["rate_limits"]["five_hour"] = {**window, "used_percentage": 99}
        self.run_helper("statusline", FIRST, data=payload, env=other_env)
        self.assertNotIn("quota", json.loads(self.run_helper("snapshot", FIRST, env=other_env).stdout))
        self.assertEqual(json.loads(self.run_helper("quotas", FIRST).stdout)[FIRST]["five_hour"], window)
        self.assertNotEqual(self.run_helper("run", FIRST, env=other_env, check=False).returncode, 0)
        self.assertNotEqual(self.run_helper("quotas", "../outside", check=False).returncode, 0)

    def test_missing_window_keeps_its_observation_time(self):
        now = int(time.time())
        with patch.dict(os.environ, self.env):
            base = helper.profile(FIRST)
            base.mkdir(parents=True)
            week = {"used_percentage": 40, "resets_at": now + 86400}
            # Legacy snapshot: its timestamp applied to both windows.
            (base / "quota.json").write_text(json.dumps({
                "identity": self.subject, "observed_at": now - 3600, "seven_day": week,
            }))
            helper.save_usage(base, {"rate_limits": {"five_hour": {
                "used_percentage": 20, "resets_at": now + 18000,
            }}}, self.subject, lambda: self.subject)
            quota = helper.read_quota(base)
        self.assertEqual(quota["seven_day"]["observed_at"], now - 3600)
        self.assertGreaterEqual(quota["five_hour"]["observed_at"], now)

    def test_cli_arguments_and_existing_statusline(self):
        args = self.run_helper("run", FIRST, "--model", "sonnet").stdout.splitlines()
        self.assertEqual(args[-2:], ["--model", "sonnet"])
        self.assertIn("statusline " + FIRST, json.loads(args[1])["statusLine"]["command"])
        config = self.home / ".local/share/cage/claude-accounts" / FIRST / "config"
        original = '{"statusLine":{"type":"command","command":"my-original-status"}}'
        (config / "settings.json").write_text(original)
        args = self.run_helper("run", FIRST, "--model", "sonnet").stdout.splitlines()
        self.assertEqual(args, ["--model", "sonnet"])
        self.assertEqual((config / "settings.json").read_text(), original)

    def test_default_reuses_the_existing_configuration_and_pin(self):
        home_config = self.home / ".claude"
        self.assertEqual(json.loads(self.run_helper("snapshot", "default").stdout), {"logged_in": False})
        self.assertFalse(home_config.exists())
        home_config.mkdir()
        history = home_config / "example-history"
        history.write_text("previous conversation")
        snap = lambda env: json.loads(self.run_helper("snapshot", "default", env=env).stdout)
        self.assertEqual(snap(self.env)["email"], self.email)
        self.assertFalse((self.home / ".local/share/cage/claude-accounts/default").exists())
        payload = {"rate_limits": {"five_hour": {"used_percentage": 10, "resets_at": int(time.time()) + 3600}}}
        self.run_helper("statusline", "default", data=payload)
        self.assertIn("quota", snap(self.env))
        changed = {**self.env, "TEST_EMAIL": "b@example.test"}
        self.assertNotIn("quota", snap(changed))
        self.assertNotEqual(self.run_helper("run", "default", env=changed, check=False).returncode, 0)
        # Re-observing a different identity in an old process may not assign B's
        # quota to A, nor change the underlying config or history.
        payload["rate_limits"]["five_hour"]["used_percentage"] = 99
        self.run_helper("statusline", "default", env=changed, data=payload)
        self.assertEqual(snap(self.env)["quota"]["five_hour"]["used_percentage"], 10)
        self.assertNotEqual(self.run_helper("login", "default", check=False).returncode, 0)
        self.assertEqual(history.read_text(), "previous conversation")
        for override in ({"ANTHROPIC_API_KEY": "fixture-api-key"}, {"CLAUDE_CONFIG_DIR": str(self.home / "different")},
                         {"ANTHROPIC_BASE_URL": "https://example.test"}, {"TEST_PROVIDER": "gateway"}):
            self.assertEqual(snap({**self.env, **override}), {"logged_in": False})
        unknown = self.run_helper("snapshot", "default", env={**self.env, "TEST_CONFIG_RESULT": str(self.home / "different")}, check=False)
        self.assertNotEqual(unknown.returncode, 0)

    def test_cancel_login_closes_browser_even_when_cli_fails(self):
        env = {**self.env}
        with patch.dict(os.environ, env, clear=True), patch.object(helper, "OPEN", "/bin/true"), \
                patch.object(helper.subprocess, "call", side_effect=KeyboardInterrupt), \
                patch.object(helper.subprocess, "run") as run:
            with self.assertRaises(KeyboardInterrupt):
                helper.main(["login", FIRST])
            self.assertEqual(run.call_args.args[0], ["/bin/true", "--finish-login"])
            self.assertEqual(run.call_args.kwargs["env"]["CAGE_CLAUDE_LOGIN"], FIRST)

    def install_usage_fixture(self):
        cli = self.home / ".local/bin/claude"
        cli.write_text("#!" + sys.executable + "\n" + """import json, os, sys
from pathlib import Path
home = Path.home()
args = sys.argv[1:]
def log(text):
    with (home / 'calls').open('a') as output: output.write(text + '\\n')
if args[-2:] == ['auth','status']:
    log('auth')
    email = os.environ['TEST_EMAIL']
    if (home / 'swapped').exists():
        email = 'b@example.test'
    print(json.dumps({'authMethod':'claude.ai','apiProvider':'firstParty','loggedIn':True,
                      'configDirectory':os.environ.get('CLAUDE_CONFIG_DIR',str(home/'.claude')),
                      'email':email,'orgId':'test-org','subscriptionType':'pro'}))
elif '-p' in args:
    request = json.loads(sys.stdin.read())
    assert request == {'type':'control_request','request_id':'quota','request':{'subtype':'get_usage','skip_behaviors':True}}
    assert '--no-session-persistence' in args and '--strict-mcp-config' in args and '--verbose' in args
    assert args[args.index('--output-format') + 1] == 'stream-json'
    assert args[args.index('--input-format') + 1] == 'stream-json'
    assert args[args.index('--tools') + 1] == ''
    assert json.loads(args[args.index('--settings')+1])['disableAllHooks'] is True
    assert Path.cwd() != home and not Path.cwd().is_relative_to(home)
    log('usage')
    if os.environ.get('SWAP') == '1':
        (home / 'swapped').touch()
    data = {'rate_limits_available':os.environ.get('AVAILABLE') != 'false',
            'rate_limits':{'limits':json.loads(os.environ['ROWS']), **json.loads(os.environ.get('LEGACY','{}'))},
            'session':{'total_cost_usd':0,'model_usage':{}}, 'private':'private-raw-output'}
    print(json.dumps({'type':'control_response','response':{'request_id':'quota','subtype':'success','response':data}}))
    if os.environ.get('TURNS'):
        print(json.dumps({'type':'result','num_turns':int(os.environ['TURNS']),'is_error':False}))
    sys.exit(int(os.environ.get('EXIT','0')))
else:
    sys.exit(7)
""")
        cli.chmod(0o700)

    def test_official_usage_windows_are_subject_bound_and_independently_cached(self):
        self.install_usage_fixture()
        reset = int(time.time()) + 18000
        date = datetime.fromtimestamp(reset, timezone.utc).isoformat()
        weekly = {"kind": "weekly_all", "percent": 34.5, "resets_at": date}
        session = {"kind": "session", "percent": 0, "resets_at": None}
        def query(rows, account="default", **extra):
            env = {**self.env, "ROWS": json.dumps(rows), **extra}
            result = self.run_helper("usage", account, self.subject, env=env)
            self.assertNotIn("private-raw-output", result.stdout + result.stderr)
            return json.loads(result.stdout)
        (self.home / ".claude").mkdir()
        first = query([weekly, session, {"kind":"weekly_scoped","percent":99,"resets_at":date}])
        self.assertEqual(first["usage_status"], "reported")
        self.assertEqual(first["quota"]["seven_day"]["resets_at"], reset)
        self.assertEqual(first["quota"]["five_hour"]["used_percentage"], 0)
        self.assertEqual(first["quota"]["five_hour"]["resets_at"], 0)
        # A passive redraw doesn't refresh either age. A completed control
        # query refreshes only the window actually returned by the CLI.
        path = self.home / ".local/share/cage/claude-accounts/default/quota.json"
        stored = json.loads(path.read_text())
        old = int(time.time()) - 300
        stored["observed_at"] = stored["five_hour"]["observed_at"] = stored["seven_day"]["observed_at"] = old
        path.write_text(json.dumps(stored))
        self.run_helper("statusline", "default", data={"rate_limits": {
            "five_hour": stored["five_hour"], "seven_day": stored["seven_day"],
        }})
        self.assertEqual(json.loads(path.read_text()), stored)
        partial = query([weekly])
        self.assertEqual(partial["usage_status"], "partial")
        self.assertGreater(partial["usage_checked_at"], old)
        self.assertEqual(partial["quota"]["five_hour"]["observed_at"], old)
        self.assertGreater(partial["quota"]["seven_day"]["observed_at"], old)
        stored = json.loads(path.read_text())
        self.assertEqual(query([], FIRST)["usage_status"], "unavailable")
        self.assertEqual(query(None)["usage_status"], "cached")
        self.assertEqual(query(None, AVAILABLE="false")["usage_status"], "not_applicable")
        self.assertEqual(query([weekly], EXIT="1")["usage_status"], "error")
        self.assertEqual(query([weekly], TURNS="1")["usage_status"], "error")
        self.assertEqual(json.loads(path.read_text()), stored)
        changed = query([dict(session, percent=91)], SWAP="1")
        self.assertEqual(changed["usage_status"], "identity_changed")
        self.assertNotIn("quota", changed)
        self.assertEqual(json.loads(path.read_text()), stored)
        calls = self.home / "calls"
        calls.write_text("")
        self.assertEqual(query([weekly])["usage_status"], "identity_changed")
        self.assertEqual(calls.read_text().splitlines(), ["auth"])

    def test_legacy_control_windows_and_row_fallback(self):
        self.install_usage_fixture()
        now = int(time.time())
        stamp = datetime.fromtimestamp(now + 3600, timezone.utc).isoformat()
        legacy = {"five_hour":{"utilization":45.5,"resets_at":stamp}, "seven_day":{"utilization":None,"resets_at":stamp},
                  "seven_day_sonnet":{"utilization":98,"resets_at":stamp}}
        rows = [{"kind":"session","percent":12,"resets_at":stamp},{"kind":"weekly_all","percent":67,"resets_at":None}]
        env = {**self.env,"ROWS":json.dumps(rows),"LEGACY":json.dumps(legacy)}
        observed = json.loads(self.run_helper("usage", FIRST, self.subject, env=env).stdout)
        self.assertEqual(observed["usage_status"], "reported")
        self.assertEqual(observed["quota"]["five_hour"]["used_percentage"], 45.5)
        self.assertEqual(observed["quota"]["five_hour"]["resets_at"], now + 3600)
        self.assertEqual(observed["quota"]["seven_day"]["used_percentage"], 67)
        self.assertEqual(observed["quota"]["seven_day"]["resets_at"], 0)


if __name__ == "__main__":
    unittest.main()
