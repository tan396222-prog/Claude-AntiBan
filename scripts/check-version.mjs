import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
const root=fileURLToPath(new URL('../',import.meta.url));
const version=fs.readFileSync(path.join(root,'VERSION'),'utf8').trim();
assert.match(version,/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/);
for(const directory of ['ui','www']){
 const pkg=JSON.parse(fs.readFileSync(path.join(root,directory,'package.json'),'utf8'));
 const lock=JSON.parse(fs.readFileSync(path.join(root,directory,'package-lock.json'),'utf8'));
 for(const actual of [pkg.version,lock.version,lock.packages[''].version]) assert.equal(actual,version,`${directory}: version differs from VERSION`);
}
console.log(`Application package versions match VERSION: ${version}`);
