import { execFileSync } from 'node:child_process';
import http from 'node:http';
import { createWorkbench } from './workbench-fixture.mjs';
import { createPrivacyController } from '../../scripts/browser-privacy-fixture.mjs';
export async function createSettingsWorkbench(options = {}) {
  const clientContext = options.clientContext || { mode: 'local', connection_id: 'local', client_platform: 'darwin', server_platform: 'darwin', connection: { state: 'disconnected' } };
  const controller = await createPrivacyController();
  const fixtureState = { failSave: false, home: controller.origin + '/home' };
  const archive = execFileSync('uv', ['run', '--no-project', 'python', '-c', `import io,json,sys,zipfile
b=io.BytesIO()
with zipfile.ZipFile(b,'w') as z:
 z.writestr('manifest.json',json.dumps({'manifest_version':3,'name':'阅读助手','version':'1.0','description':'调整网页阅读体验。','permissions':['storage'],'content_scripts':[{'matches':['http://localhost/*'],'js':['content.js']}]}))
 z.writestr('content.js','document.documentElement.dataset.cageSettingsExtension="active";')
sys.stdout.buffer.write(b.getvalue())`], { stdio: ['ignore', 'pipe', 'ignore'], maxBuffer: 1 << 20 });
  const profileID = 'a'.repeat(32), extensionID = 'b'.repeat(32);
  // WebKit's routing protocol omits Blob upload bodies. Keep those uploads on
  // real HTTP, so the controller receives the actual File bytes in both engines.
  const proxy = http.createServer(async (request, response) => {
    if (request.url === '/api/browser/extensions/upload' && request.method === 'POST') {
      try {
        const chunks = []; for await (const chunk of request) chunks.push(chunk);
        const result = await controller.uploadExtension(Buffer.concat(chunks));
        response.setHeader('Content-Type', 'application/json'); response.end(JSON.stringify(result));
      } catch (error) { response.writeHead(400); response.end(error.message); }
      return;
    }
    if (request.url.startsWith('/api/')) { response.writeHead(404); response.end(); return; }
    const target = new URL(request.url, process.env.CAGE_UI_URL || 'http://127.0.0.1:5198');
    const upstream = http.request(target, { headers: { ...request.headers, host: target.host } }, source => { response.writeHead(source.statusCode, source.headers); source.pipe(response); });
    upstream.on('error', error => { response.writeHead(502); response.end(error.message); }); request.pipe(upstream);
  });
  await new Promise(resolve => proxy.listen(0, '127.0.0.1', resolve));
  let workbench;
  try {
    workbench = await createWorkbench({ ...options, clientContext, url: `http://127.0.0.1:${proxy.address().port}`,
      browserRequest: async (route, path, body) => {
        if (path === '/browser/extensions/upload') return route.continue();
        if (path === '/browser/resize') return route.fulfill({ json: { ok: true } });
        if (path === '/browser/preferences') {
          if (body) {
            if (fixtureState.failSave) return route.fulfill({ status: 500, contentType: 'text/plain; charset=utf-8', body: '保存浏览器设置失败，请重试' });
            await controller.api('/homepage', { url: body.browser_home_url }); fixtureState.home = body.browser_home_url;
          }
          return route.fulfill({ json: { browser_home_url: fixtureState.home } });
        }
        try {
          const result = await controller.api(path.replace('/browser', ''), body);
          return route.fulfill({ json: result });
        } catch (error) { return route.fulfill({ status: 400, contentType: 'text/plain; charset=utf-8', body: error.message }); }
      },
      clientRequest: async (route, path, body) => {
        if (path === '/client/context') return route.fulfill({ json: clientContext });
        if (path === '/client/browser-extensions') {
          if (body) return route.fulfill({ contentType: 'application/zip', body: archive });
          return route.fulfill({ json: { profiles: [{ id: profileID, browser: 'Microsoft Edge', name: '个人配置', extensions: [
            { id: extensionID, name: '阅读助手', version: '1.0', enabled: false, state_known: true, compatible: true, identity_preserved: false, permissions: ['storage'], hostPermissions: ['http://localhost/*'] },
            { id: 'c'.repeat(32), name: '旧版扩展', version: '2.0', enabled: true, state_known: true, compatible: false, identity_preserved: false, permissions: [], hostPermissions: [] },
          ] }] } });
        }
        return route.fulfill({ json: path === '/client/hosts' ? { hosts: [] } : {} });
      },
    });
    workbench.state.status.ready = workbench.state.status.vm_running = true;
    workbench.state.settings.browser_home_url = fixtureState.home;
    await workbench.page.reload();
    return { ...workbench, controller, fixtureState, archive, profileID, extensionID, close: async () => { await workbench.browser.close(); await new Promise(resolve => proxy.close(resolve)); await controller.close(); } };
  } catch (error) { await workbench?.browser.close(); await new Promise(resolve => proxy.close(resolve)); await controller.close(); throw error; }
}
