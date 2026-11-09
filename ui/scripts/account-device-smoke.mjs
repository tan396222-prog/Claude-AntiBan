import assert from 'node:assert/strict';
import {createWorkbench} from './workbench-fixture.mjs';
let fixture;
const calls=[];
let cancelBiometric=false;
let unavailable=false, supported=true, legacyLauncher=false, failEnrollment=false, sequence=0;
const keychain=new Map();
fixture=await createWorkbench({nativeRequest:async(body)=>{
 if(body.action!=='accountDevice')return null;
 const {operation,kind}=body;
 const a=fixture?.state.account || {};a.devices ||= {};
 const binding=a.device_binding || 'original-directory';
 calls.push({operation,kind,binding});
 if(operation==='status'&&legacyLauncher)return {supported:true,biometric_available:true};
 if(operation==='status')return {supported,biometric_available:true,device_status:Object.fromEntries(['automatic','biometric'].map(k=>[
   k,!a.devices[k]?'disabled':unavailable?'unavailable':keychain.get(binding+'.'+k)===a.devices[k]?(k==='biometric'?'authentication_required':'available'):'missing',
 ]))};
 if(operation==='enable'){
   a.devices[kind]='fixture-'+kind+'-'+(++sequence);
   if(failEnrollment){delete a.devices[kind];throw Error('钥匙串保存失败');}
   keychain.set(binding+'.'+kind,a.devices[kind]);
 }
 if(operation==='disable'){delete a.devices[kind];keychain.delete(binding+'.'+kind);}
 if(operation==='unlock'){
   assert.equal(keychain.get(binding+'.'+kind),a.devices[kind],'attempted unlock with missing credentials');
   if(kind==='biometric'&&cancelBiometric)throw Error('已取消指纹验证，可继续使用密码');
   if(kind==='automatic'&&a.require_authentication)throw Error('手动锁定');
   a.unlocked=true;a.require_authentication=false;
 }
 return {ok:true};
}});
const {page,state,browser,errors}=fixture;
const menu=()=>page.getByRole('button',{name:/^账户菜单，/});
try {
 Object.assign(state.account,{exists:false,unlocked:false,username:'',devices:{}});
 await page.reload();
 const defaultRemember=page.getByRole('checkbox',{name:/记住登录/});
 await defaultRemember.waitFor();assert.equal(await defaultRemember.isChecked(),true);
 await page.getByLabel('用户名',{exact:true}).fill('林间工作室');
 await page.getByLabel('设置密码',{exact:true}).fill('fixture-password');
 await page.getByLabel('确认密码',{exact:true}).fill('fixture-password');
 await page.getByRole('button',{name:'创建本地账户',exact:true}).click();await menu().waitFor();
 await menu().click();await page.getByRole('menuitem',{name:'设置',exact:true}).click();
 const automatic=page.getByRole('checkbox',{name:/记住登录/});
 const biometric=page.getByRole('checkbox',{name:/使用 Touch ID 解锁/});
 await page.waitForFunction(()=>document.querySelector('.account-device-settings input')?.checked);
 assert.equal(await automatic.isChecked(),true);
 await biometric.click();await page.getByRole('status').filter({hasText:'已开启'}).waitFor();
 assert.ok(state.account.devices.automatic&&state.account.devices.biometric);
 await page.screenshot({path:'.oil-ui/local-account/shots/device-settings.png'});
 // A genuine reopen keeps enrollment but starts with a locked server.
 state.account.unlocked=false;await page.reload();await menu().waitFor();
 assert.equal(calls.filter(x=>x.operation==='unlock'&&x.kind==='automatic').length,1);
 await menu().click();await page.getByRole('menuitem',{name:'设置',exact:true}).click();
 await automatic.click();await page.getByRole('status').filter({hasText:'已关闭'}).waitFor();
 await page.reload();await menu().click();await page.getByRole('menuitem',{name:'设置',exact:true}).click();
 assert.equal(await automatic.isChecked(),false,'explicit opt-out was overridden');
 await automatic.click();await page.getByRole('status').filter({hasText:'已开启'}).waitFor();
 await page.getByRole('button',{name:'返回工作台',exact:true}).click();
 await menu().click();await page.getByRole('menuitem',{name:/锁定 Cage/}).click();
 await page.getByRole('region',{name:'解锁 Cage',exact:true}).waitFor();
 await page.reload();await page.getByRole('button',{name:'使用 Touch ID 解锁',exact:true}).waitFor();
 assert.equal(calls.filter(x=>x.operation==='unlock'&&x.kind==='automatic').length,1);
 cancelBiometric=true;await page.getByRole('button',{name:'使用 Touch ID 解锁',exact:true}).click();
 await page.getByRole('alert').filter({hasText:'已取消指纹验证'}).waitFor();
 assert.equal(state.account.unlocked,false);
 await page.screenshot({path:'.oil-ui/local-account/shots/touch-id-fallback.png'});
 cancelBiometric=false;await page.getByRole('button',{name:'使用 Touch ID 解锁',exact:true}).click();await menu().waitFor();

 // Copying the account directory preserves server IDs but not native bindings.
 // Stop the old page first, just as directory migration requires stopping Cage.
 const workbenchURL=page.url();
 await page.goto('about:blank');
 const oldDevices=structuredClone(state.account.devices);
 const unlocksBeforeMigration=calls.filter(x=>x.operation==='unlock').length;
 Object.assign(state.account,{device_binding:'migrated-directory',unlocked:false,require_authentication:false});
 await page.goto(workbenchURL);
 await page.getByRole('status').filter({hasText:'当前目录的快捷登录凭据缺失'}).waitFor();
 assert.equal(calls.filter(x=>x.operation==='unlock').length,unlocksBeforeMigration,JSON.stringify(calls));
 assert.equal(await page.getByRole('button',{name:'使用 Touch ID 解锁',exact:true}).count(),0);
 await page.reload();await page.getByRole('status').filter({hasText:'当前目录的快捷登录凭据缺失'}).waitFor();
 assert.equal(calls.filter(x=>x.operation==='unlock').length,unlocksBeforeMigration,'reopen retried invalid enrollment');
 await page.getByLabel('账户密码',{exact:true}).fill('fixture-password');
 await page.getByRole('button',{name:'解锁 Cage',exact:true}).click();await menu().waitFor();
 await menu().click();await page.getByRole('menuitem',{name:'设置',exact:true}).click();
 await page.getByText('当前目录的凭据缺失或已失效，请重新开启。',{exact:true}).first().waitFor();
 assert.equal(await automatic.isChecked(),false);assert.equal(await biometric.isChecked(),false);
 assert.deepEqual(state.account.devices,oldDevices,'checking status mutated authorization');
 await page.locator('.account-device-settings').screenshot({path:'.oil-ui/local-account/shots/device-migrated.png'});
 failEnrollment=true;await automatic.click();await page.getByRole('alert').filter({hasText:'钥匙串保存失败'}).waitFor();
 await automatic.waitFor();assert.equal(await automatic.isChecked(),false);assert.equal(state.account.devices.automatic,undefined);
 failEnrollment=false;await automatic.click();await page.getByRole('status').filter({hasText:'已开启'}).waitFor();
 await page.waitForFunction(()=>document.querySelector('.account-device-settings input')?.checked);
 await biometric.click();await page.getByRole('status').filter({hasText:'已开启'}).waitFor();
 await page.waitForFunction(()=>[...document.querySelectorAll('.account-device-settings input')].every(e=>e.checked));
 assert.equal(keychain.get('original-directory.automatic'),oldDevices.automatic,'repair touched original binding');
 state.account.unlocked=false;await page.reload();await menu().waitFor();
 assert.equal(calls.filter(x=>x.operation==='unlock').length,unlocksBeforeMigration+1,'repaired enrollment did not unlock');

 // A temporarily inaccessible Keychain is not missing enrollment.
 unavailable=true;await page.reload();await menu().click();await page.getByRole('menuitem',{name:'设置',exact:true}).click();
 await page.getByRole('button',{name:'重新检查快捷登录'}).waitFor();
 assert.equal(await automatic.isChecked(),false);assert.equal(await automatic.isDisabled(),true);
 const savedDevices=structuredClone(state.account.devices);
 unavailable=false;await page.getByRole('button',{name:'重新检查快捷登录'}).click();
 await page.waitForFunction(()=>document.querySelector('.account-device-settings input')?.checked);
 assert.deepEqual(state.account.devices,savedDevices);
 // An older launcher can save credentials but cannot report their availability.
 // Do not present unchecked toggles that repeatedly re-enroll successfully.
 legacyLauncher=true;await page.reload();await menu().click();await page.getByRole('menuitem',{name:'设置',exact:true}).click();
 await page.getByRole('status').filter({hasText:'当前应用启动器需要更新'}).waitFor();
 assert.equal(await automatic.count(),0);assert.equal(await biometric.count(),0);
 assert.deepEqual(state.account.devices,savedDevices);
 legacyLauncher=false;
 // Linux/Windows expose no native quick-login capability even with copied records.
 supported=false;state.account.unlocked=false;await page.reload();
 await page.getByLabel('账户密码',{exact:true}).waitFor();
 assert.equal(await page.getByRole('button',{name:'使用 Touch ID 解锁',exact:true}).count(),0);
 assert.equal(calls.filter(x=>x.operation==='unlock').length,unlocksBeforeMigration+1);
 await page.getByLabel('账户密码',{exact:true}).fill('fixture-password');
 await page.getByRole('button',{name:'解锁 Cage',exact:true}).click();await menu().waitFor();
 await menu().click();await page.getByRole('menuitem',{name:'设置',exact:true}).click();
 assert.equal(await automatic.count(),0);
 supported=true;await page.reload();await menu().waitFor();
 // A locked response from a restarted controller invalidates cached availability
 // before the refreshed account (with a different binding) reaches the page.
 const beforeRestart=calls.filter(x=>x.operation==='unlock').length;
 Object.assign(state.account,{device_binding:'restarted-directory',unlocked:false,require_authentication:false});
 await page.evaluate(()=>window.dispatchEvent(new Event('cage-account-locked')));
 await page.getByRole('status').filter({hasText:'当前目录的快捷登录凭据缺失'}).waitFor();
 assert.equal(calls.filter(x=>x.operation==='unlock').length,beforeRestart,'locked response reused old availability');
 await page.getByLabel('账户密码',{exact:true}).fill('fixture-password');
 await page.getByRole('button',{name:'解锁 Cage',exact:true}).click();await menu().waitFor();
 await menu().click();await page.getByRole('menuitem',{name:'退出账户',exact:true}).click();
 await page.getByRole('dialog').getByRole('button',{name:'停止环境并退出账户',exact:true}).click();
 await page.getByRole('region',{name:'解锁 Cage',exact:true}).waitFor();await page.reload();
 await page.getByLabel('账户密码',{exact:true}).waitFor();assert.deepEqual(state.account.devices,{});
 assert.equal(await page.getByRole('button',{name:'使用 Touch ID 解锁',exact:true}).count(),0);
 assert.deepEqual(errors,[]);
 console.log('PASS: enrollment, reopen, manual lock, cancellation, migration, repair, failed save, temporary Keychain failure, unsupported platforms and logout');
}finally{await browser.close();}
