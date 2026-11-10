import importlib.util
import json
import os
from pathlib import Path
import sys
import tempfile
import subprocess
import types
import unittest
from unittest.mock import patch

source=Path(__file__).with_name('mobile_cli_identity.py')
spec=importlib.util.spec_from_file_location('ccs_identity_policy',source)
identity=importlib.util.module_from_spec(spec);spec.loader.exec_module(identity)

class CCSPolicyTests(unittest.TestCase):
    def test_mcp_user_servers_are_preserved_and_cage_or_channel_collisions_refused(self):
        args=['--mcp-config',json.dumps({'mcpServers':{'user-server':{'command':'fixture'}}}),'--model','fixture-model']
        before=args.copy();identity.validate_shared_mcp(args);self.assertEqual(args,before)
        for bad in [
            ['--mcp-config',json.dumps({'mcpServers':{'cage':{'command':'existing'}}})],
            ['--channels','server:user'],['--dangerously-load-development-channels','server:user']]:
            with self.assertRaises(identity.Unverified):identity.validate_shared_mcp(bad)
        with tempfile.TemporaryDirectory() as folder:
            config=Path(folder)/'.mcp.json';config.write_text(json.dumps({'mcpServers':{'cage':{}}}))
            with self.assertRaises(identity.Unverified):identity.validate_shared_mcp([], [config])
    def test_windows_registry_settings_read_exact_policy_value_and_do_not_ignore_bad_json(self):
        class Key:
            def __enter__(self):return self
            def __exit__(self,*args):pass
        calls=[]
        registry=types.SimpleNamespace(HKEY_LOCAL_MACHINE=1,HKEY_CURRENT_USER=2,KEY_WOW64_64KEY=4,KEY_WOW64_32KEY=8,KEY_READ=16,REG_SZ=1,REG_EXPAND_SZ=2,
            OpenKey=lambda hive,key,zero,rights:(calls.append((hive,key,rights)) or Key()),QueryValueEx=lambda key,name:('{"channelsEnabled":false}',1),ExpandEnvironmentStrings=lambda value:value)
        with patch.dict(sys.modules,{'winreg':registry}),patch.object(os,'name','nt'):
            self.assertEqual(identity.windows_registry_settings(),[{'channelsEnabled':False}])
            self.assertEqual(len(calls),4);self.assertTrue(all(call[1]==r'SOFTWARE\Policies\ClaudeCode' for call in calls))
            registry.QueryValueEx=lambda key,name:('invalid-json',1)
            with self.assertRaises(identity.Unverified):identity.windows_registry_settings()
    def test_actual_ccs_config_and_pinned_model_permissions_use_the_same_auth_snapshot(self):
        with tempfile.TemporaryDirectory() as folder:
            root=Path(folder);config=root/'actual-config';config.mkdir()
            (config/'settings.json').write_text(json.dumps({'theme':'dark'}))
            profile={'env':{'ANTHROPIC_API_KEY':'fixture-only','ANTHROPIC_BASE_URL':'http://127.0.0.1:45678'},'model':'profile-model','permissions':{'deny':['Bash(profile)']}}
            env={'CLAUDE_CONFIG_DIR':str(config),'DYNAMIC_PROXY_TOKEN':'fixture-parent-only'}
            seen=[]
            def auth(args,**options):
                seen.append((args,options))
                # A mutable source change after auth must not affect the turn.
                (config/'settings.json').write_text('{"model":"changed-after-auth"}')
                return subprocess.CompletedProcess(args,0,json.dumps({'loggedIn':True,'apiProvider':'firstParty','configDirectory':str(config),'authMethod':'api_key','apiKeySource':'ANTHROPIC_API_KEY'}).encode())
            with tempfile.NamedTemporaryFile() as snapshot,patch.object(identity,'managed_settings',return_value=[]),patch.object(identity.subprocess,'run',side_effect=auth):
                binary,args,result_env,material=identity.resolve_actual_context('trusted-native.exe',['ccs','qa'],['--setting-sources','user','--session-id','fixture'],env,profile,snapshot)
                snapshot.seek(0);frozen=json.load(snapshot)
                self.assertEqual(frozen['model'],'profile-model')
                self.assertEqual(frozen['permissions']['deny'],['Bash(profile)']);self.assertEqual(frozen['theme'],'dark')
                self.assertEqual(args[:4],seen[0][0][1:5]);self.assertEqual(result_env,env)
                self.assertEqual(material[1],str(config.resolve()))
            with self.assertRaises(identity.Unverified):
                identity.merged_settings({'permissions':{'deny':['Bash(user)']}},profile)
    def test_managed_registry_routing_is_refused_and_disabled_channels_remain_disabled(self):
        with patch.object(identity,'windows_registry_settings',return_value=[{'env':{'ANTHROPIC_BASE_URL':'https://changed.invalid'}}]):
            with self.assertRaises(identity.Unverified):identity.managed_settings()
        with patch.object(identity,'windows_registry_settings',return_value=[{'channelsEnabled':False}]):
            self.assertTrue(identity.shared_channels_disabled())
    def test_actual_global_and_current_project_mcp_collisions_are_refused(self):
        with tempfile.TemporaryDirectory() as folder:
            config=Path(folder)/'actual-config';config.mkdir();project=Path(folder)/'project';project.mkdir()
            filename=config/'.claude.json';env={'CLAUDE_CONFIG_DIR':str(config)}
            valid={'mcpServers':{'user-server':{}},'projects':{str(project):{'mcpServers':{'project-server':{}}},str(Path(folder)/'other'):{'mcpServers':{'cage':{}}}}}
            filename.write_text(json.dumps(valid));before=filename.read_bytes()
            identity.validate_shared_mcp([],config_env=env,workspace=project);self.assertEqual(filename.read_bytes(),before)
            for bad in [{**valid,'mcpServers':{'cage':{}}},{**valid,'projects':{str(project):{'mcpServers':{'cage':{}}}}}]:
                filename.write_text(json.dumps(bad))
                with self.assertRaises(identity.Unverified):identity.validate_shared_mcp([],config_env=env,workspace=project)

if __name__=='__main__':unittest.main()
