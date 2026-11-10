"""Native Windows smoke: real private socket, MCP and hook entry points.

Run with the staged uv Python. This uses only its own temporary profile and
never reads an existing Claude login or sends a model request.
"""
import concurrent.futures
import importlib.util
import json
import os
from pathlib import Path
import queue
import subprocess
import sys
import tempfile
import threading
import unittest
import uuid


@unittest.skipUnless(sys.platform == 'win32', 'requires native Windows Python')
class NativeChannelTests(unittest.TestCase):
    def test_live_snapshot_is_readable_by_another_native_process_and_cleaned(self):
        spec=importlib.util.spec_from_file_location('windows_snapshot_identity',Path(__file__).with_name('mobile_cli_identity.py'))
        identity=importlib.util.module_from_spec(spec);spec.loader.exec_module(identity)
        from unittest.mock import patch
        with tempfile.TemporaryDirectory(prefix='cage-ccs-snapshot-') as folder:
            file=tempfile.NamedTemporaryFile(dir=folder,suffix='.settings.json',delete=False)
            try:
                payload={'env':{'ANTHROPIC_API_KEY':'fixture-only'},'model':'fixture-model','permissions':{'deny':['Bash(fixture)']}}
                with patch.object(identity,'managed_settings',return_value=[]):
                    identity.freeze_settings({'CLAUDE_CONFIG_DIR':folder},payload,file)
                self.assertFalse(os.get_inheritable(file.fileno()))
                result=subprocess.run([sys.executable,'-X','utf8','-I','-c','import json,sys;v=json.load(open(sys.argv[1],encoding="utf-8"));assert v["model"]=="fixture-model";assert v["permissions"]["deny"]==["Bash(fixture)"]',file.name],capture_output=True,timeout=5)
                self.assertEqual(result.returncode,0)
            finally:file.close();Path(file.name).unlink()
            self.assertEqual(list(Path(folder).iterdir()),[])
    def test_native_mcp_and_hooks_preserve_receipt_and_permission(self):
        source = Path(__file__).with_name('cli_channel.py').resolve()
        with tempfile.TemporaryDirectory(prefix='cage-cli-native-') as temporary:
            home = Path(temporary).resolve()
            connection = str(uuid.uuid4())
            state_root = home / 'channels'
            folder = state_root / connection
            folder.mkdir(parents=True)
            env = {**os.environ, 'USERPROFILE':str(home), 'CAGE_CLI_STATE_ROOT':str(state_root),
                   'CAGE_CLI_ACCOUNT_ROOT':str(home/'accounts'), 'CAGE_CLI_SHARED_ROOT':str(home/'shared')}
            (folder/'state.json').write_text(json.dumps({'cli_id':connection,'config':str(home/'.claude'),
                'workspace':str(home/'project'),'pid':os.getpid(),'busy':False,'closed':False}),encoding='utf-8')
            child = subprocess.Popen([sys.executable,'-X','utf8','-I',str(source),'serve',connection],
                env=env,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,encoding='utf-8')
            lines = queue.Queue()
            reader = threading.Thread(target=lambda:[lines.put(json.loads(line)) for line in child.stdout],daemon=True)
            reader.start()
            def send(value):
                child.stdin.write(json.dumps({'jsonrpc':'2.0',**value})+'\n');child.stdin.flush()
            def invoke(mode,value):
                result=subprocess.run([sys.executable,'-X','utf8','-I',str(source),mode,'-' if mode=='rpc' else connection],
                    env=env,input=json.dumps(value),text=True,encoding='utf-8',capture_output=True,timeout=5,check=True)
                return json.loads(result.stdout)
            def rpc(**fields): return invoke('rpc',{'cli_id':connection,'connection':connection,**fields})
            try:
                send({'id':1,'method':'initialize','params':{'protocolVersion':'2024-11-05'}})
                self.assertIn('claude/channel/permission',lines.get(timeout=5)['result']['capabilities']['experimental'])
                send({'method':'notifications/initialized'})
                request=str(uuid.uuid4());text='中文🙂\n保留 </channel> 原文'
                with concurrent.futures.ThreadPoolExecutor(2) as workers:
                    replies=list(workers.map(lambda _:rpc(action='send',request_id=request,text=text),range(2)))
                self.assertEqual(replies,[{'ok':True},{'ok':True}])
                event=lines.get(timeout=5)
                self.assertEqual(event['params']['content'],text)
                self.assertEqual(event['params']['meta']['cage_request'],request)
                with self.assertRaises(queue.Empty):lines.get(timeout=.1)
                self.assertIn('error',rpc(action='send',request_id=request,text='changed'))
                invoke('hook',{'hook_event_name':'UserPromptSubmit','session_id':connection,'cwd':str(home/'project')})
                state=json.loads((folder/'state.json').read_text(encoding='utf-8'))
                self.assertTrue(state['busy']);self.assertEqual(state['requests'][request],__import__('hashlib').sha256(text.encode()).hexdigest())
                send({'method':'notifications/claude/channel/permission_request','params':{'request_id':'native-proof','tool_name':'PowerShell','input_preview':'{"command":"Get-Date"}'}})
                import time
                until=time.monotonic()+3
                while time.monotonic()<until:
                    state=json.loads((folder/'state.json').read_text(encoding='utf-8'))
                    if state.get('approvals'):break
                    time.sleep(.01)
                approval=state['approvals'][0]['id']
                self.assertEqual(rpc(action='approve',approval_id=approval,allow=False),{'ok':True})
                self.assertEqual(lines.get(timeout=5)['params'],{'request_id':'native-proof','behavior':'deny'})
                self.assertIn('error',rpc(action='approve',approval_id=approval,allow=True))
                invoke('hook',{'hook_event_name':'Stop','session_id':connection})
                self.assertFalse(json.loads((folder/'state.json').read_text(encoding='utf-8'))['busy'])
            finally:
                child.stdin.close()
                try:child.wait(timeout=5)
                except subprocess.TimeoutExpired:child.kill();child.wait()
                reader.join(timeout=1);child.stdout.close();child.stderr.close()
            self.assertEqual(child.returncode,0)
            self.assertIsNone(json.loads((folder/'state.json').read_text(encoding='utf-8'))['channel_pid'])

    def test_process_probe_does_not_signal_an_unrelated_native_process(self):
        source=Path(__file__).with_name('windows_cli_platform.py')
        spec=importlib.util.spec_from_file_location('windows_cli_platform_test',source)
        platform=importlib.util.module_from_spec(spec);spec.loader.exec_module(platform)
        self.assertTrue(platform.process_live(os.getpid()))
        self.assertTrue(platform.process_fingerprint(os.getpid()))
        from unittest.mock import patch
        with patch.dict(os.environ,{'CAGE_CLI_BINARY':str(Path(sys.executable).with_name('claude.exe'))}):
            with self.assertRaises(ValueError):platform.terminate_cli(os.getpid())
        self.assertTrue(platform.process_live(os.getpid()))

    def test_native_child_job_closes_only_its_tree(self):
        source=Path(__file__).with_name('windows_cli_platform.py')
        spec=importlib.util.spec_from_file_location('windows_cli_job_test',source)
        platform=importlib.util.module_from_spec(spec);spec.loader.exec_module(platform)
        with tempfile.TemporaryDirectory(prefix='cage-cli-job-') as temporary:
            marker=Path(temporary)/'child-pid'
            script='import subprocess,sys,time,pathlib; child=subprocess.Popen([sys.executable,"-c","import time;time.sleep(60)"]);pathlib.Path(sys.argv[1]).write_text(str(child.pid));time.sleep(60)'
            process=platform.CliProcess([sys.executable,'-X','utf8','-c',script,str(marker)],dict(os.environ))
            try:
                import time
                until=time.monotonic()+5
                while time.monotonic()<until and not marker.exists():time.sleep(.02)
                descendant=int(marker.read_text())
                self.assertTrue(platform.process_live(descendant))
            finally:process.close()
            until=time.monotonic()+5
            while time.monotonic()<until and platform.process_live(descendant):time.sleep(.02)
            self.assertFalse(platform.process_live(descendant));self.assertTrue(platform.process_live(os.getpid()))


if __name__=='__main__':unittest.main()
