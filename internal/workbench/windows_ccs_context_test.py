"""Host lifecycle tests for actual CCS child dispatch; native Win32 is separate."""
import contextlib
import ctypes
import json
import os
from pathlib import Path
import runpy
import tempfile
import subprocess
import types
import unittest
import uuid
from unittest.mock import MagicMock, patch

with patch.object(ctypes,'WinDLL',create=True,return_value=MagicMock()):
    platform=runpy.run_path(Path(__file__).with_name('windows_cli_platform.py'))
actual=platform['launch_ccs_live']

class Unverified(Exception):pass
class Changed(Exception):pass

class ActualContextTests(unittest.TestCase):
    def exercise(self, settings, failure=None, resume=False):
        with tempfile.TemporaryDirectory() as temporary:
            root=Path(temporary);connection=str(uuid.uuid4());sid=str(uuid.uuid4());folder=root/connection;folder.mkdir()
            source=folder/'profile.settings.json';source.write_text('{"env":{"ANTHROPIC_API_KEY":"fixture"}}')
            context={'id':connection,'session_id':sid,'profile':'qa','resume':resume}
            (folder/'ccs-launch.json').write_text(json.dumps(context))
            request=folder/('ccs-actual-'+str(uuid.uuid4())+'.json')
            args=['--settings',str(source),'--session-id',sid,'--setting-sources','user','--model','fixture-model']
            request.write_text(json.dumps({**context,'actual_args':args,'settings_source':str(source),'expected_settings_source':str(source),'profile_type':'settings'}))
            state={};events=[];runs=[]
            @contextlib.contextmanager
            def state_file(_):yield state
            def resolve(binary,prefix,args,env,profile,snapshot):
                if failure:raise failure
                events.append('resolve');snapshot.write(json.dumps(settings).encode());snapshot.flush();snapshot.seek(0)
                return binary,[*args,'--settings',snapshot.name],env,{'verified':'fixture'}
            identity={'Unverified':Unverified,'Changed':Changed,'read_settings':lambda path:json.loads(path.read_text()),
                      'resolve_actual_context':resolve,'validate_shared_mcp':lambda args,paths,**kwargs:events.append('merge-check'),
                      'bind':lambda *args:events.append('bind'),'shared_channels_disabled':lambda:False}
            class Process:
                pid=42
                def __init__(self,args,env):events.append('spawn');runs.append((args,env))
                def wait(self):return 0
                def close(self):events.append('closed')
            channel={'ROOT':root,'__file__':str(Path(__file__).with_name('cli_channel.py')),'identifier':lambda value:str(uuid.UUID(value)),
                     'read_json':lambda path,default=None:json.loads(path.read_text()) if path.exists() else default,
                     'state_file':state_file,'save':lambda path,value:path.write_text(json.dumps(value))}
            with patch.dict(os.environ,{'CAGE_CLI_BINARY':'trusted-native.exe','CAGE_CLI_IDENTITY_HELPER':'fixture-identity.py','DYNAMIC_PROXY_TOKEN':'fixture-only'}),patch.object(runpy,'run_path',return_value=identity),patch.dict(actual.__globals__,{'CliProcess':Process}):
                if failure and resume:
                    with self.assertRaisesRegex(ValueError,'暂时无法核验'):actual(str(request),channel)
                else:self.assertEqual(actual(str(request),channel),0)
            self.assertFalse(request.exists());self.assertEqual(set(folder.glob('*.settings.json')),{source})
            return events,runs,state,args
    def test_failed_merge_leaves_no_persistent_identity_and_keeps_original_child_context(self):
        events,runs,state,args=self.exercise({'hooks':[]})
        self.assertNotIn('bind',events);self.assertEqual(runs[0][0][1:],args)
        self.assertTrue(state['native_ccs_unverified']);self.assertFalse(state['blocked'])
        self.assertEqual(runs[0][1]['DYNAMIC_PROXY_TOKEN'],'fixture-only')
    def test_identity_binds_after_all_merge_checks_and_before_actual_spawn(self):
        events,runs,state,args=self.exercise({'model':'fixture-model','permissions':{'deny':['Bash(fixture)']},'hooks':{}})
        self.assertLess(events.index('merge-check'),events.index('bind'));self.assertLess(events.index('bind'),events.index('spawn'))
        self.assertFalse(state['native_ccs_unverified']);self.assertEqual(runs[0][1]['DYNAMIC_PROXY_TOKEN'],'fixture-only')
    def test_auth_probe_timeout_keeps_new_terminal_original_context_without_binding(self):
        events,runs,state,args=self.exercise({},subprocess.TimeoutExpired('fixture-native-auth',12))
        self.assertNotIn('bind',events);self.assertEqual(runs[0][0][1:],args)
        self.assertTrue(state['native_ccs_unverified'])
    def test_auth_probe_timeout_refuses_resume_and_cleans_snapshot_before_spawn(self):
        events,runs,state,args=self.exercise({},subprocess.TimeoutExpired('fixture-native-auth',12),True)
        self.assertEqual(runs,[]);self.assertNotIn('bind',events)

if __name__=='__main__':unittest.main()
