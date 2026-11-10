import assert from 'node:assert/strict';
import {execFileSync} from 'node:child_process';
import {createHash} from 'node:crypto';
import {chmod,mkdir,mkdtemp,readFile,readdir,rm,writeFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import test from 'node:test';
const collector=fileURLToPath(new URL('./bundle-qemu-sources.sh',import.meta.url));
test('source collector includes pinned firmware and authenticated package sources with a complete manifest',async t=>{
 const root=await mkdtemp(join(tmpdir(),'cage-qemu-source-test-'));t.after(()=>rm(root,{recursive:true,force:true}));
 const repository=join(root,'repository'),runtime=join(root,'runtime'),bin=join(root,'bin'),artifact=join(root,'bundle.tar.gz'),keyring=join(root,'installed-keyring');
 for(const path of [bin,runtime,keyring,join(repository,'packaging/windows'),join(repository,'scripts'),join(repository,'patches/qemu')])await mkdir(path,{recursive:true});
 for(const name of ['pubring.gpg','trustdb.gpg','private-keys-must-not-copy'])await writeFile(join(keyring,name),name);
 const sha='a'.repeat(40),content='fixture source archive\n';
 await writeFile(join(repository,'packaging/windows/dependencies.json'),JSON.stringify({qemuSource:{repository:'https://source.invalid/locked-qemu.git',commit:sha},qemuKeycodemap:{url:'https://source.invalid/keymap.tar.gz',hash:createHash('sha256').update(content).digest('hex')}}));
 for(const name of ['build-source-packages.txt','build-packages.txt','cage-build.json'])await writeFile(join(runtime,name),name==='build-source-packages.txt'?'mingw-w64-fixture 1.2.3-1\n':'fixture');
 for(const name of ['build-qemu-windows.sh','build-qemu-windows.ps1','bundle-qemu-sources.sh'])await writeFile(join(repository,'scripts',name),'fixture build input');
 await writeFile(join(repository,'patches/qemu/fixture.patch'),'fixture patch');
 const command=async(name,body)=>{const path=join(bin,name);await writeFile(path,'#!/bin/bash\nset -e\n'+body+'\n');await chmod(path,0o755)};
 await command('cygpath','printf "%s\\n" "$2"');
 await command('node','echo "The collector must use the explicit CAGE_NODE path" >&2; exit 99');
 await command('pacman-conf','[[ "$1" == gpgdir ]]; printf "%s\\n" "$SOURCE_TEST_KEYRING"');
 await command('git',`if [[ "$*" == *' init '* ]];then mkdir -p "\${@: -1}/.git";fi
 if [[ "$*" == *' fetch '* ]];then [[ "$*" == *'fetch --depth=1 https://source.invalid/locked-qemu.git ${sha}' ]] || exit 98;fi
 if [[ "$*" == *rev-parse* ]];then printf '${sha}\\n';fi
 if [[ "$*" == *'submodule update'* ]];then for ((i=1;i<=$#;i++));do if [[ "\${!i}" == -C ]];then j=$((i+1));q="\${!j}";fi;done;mkdir -p "$q/roms/seabios";echo 'firmware code' > "$q/roms/seabios/code.c";echo 'gitdir: private' > "$q/roms/seabios/.git";fi
 if [[ "$*" == *'submodule status'* ]];then printf ' ${sha} roms/seabios\\n';fi`);
 await command('curl',`while [[ $# -gt 0 ]];do if [[ "$1" == -o ]];then shift;destination=$1;fi;shift;done;printf '${content.replaceAll('\n','\\n')}' > "$destination"`);
 await command('pacman-key','[[ "$1" == --gpgdir && "$3" == --verify && "$2" != "$SOURCE_TEST_KEYRING" ]]; [[ $(cat "$2/pubring.gpg") == pubring.gpg && $(cat "$2/trustdb.gpg") == trustdb.gpg ]]; [[ ! -e "$2/private-keys-must-not-copy" ]]; grep -qx lock-never "$2/gpg.conf"; printf "%s\\n" "$*" >> "$SOURCE_TEST_VERIFICATIONS"');
 execFileSync('bash',[collector,repository,runtime,artifact],{env:{...process.env,PATH:bin+':'+process.env.PATH,CAGE_NODE:process.execPath,SOURCE_TEST_KEYRING:keyring,SOURCE_TEST_VERIFICATIONS:join(root,'verified')},stdio:'pipe'});
 assert.deepEqual((await readdir(keyring)).sort(),['private-keys-must-not-copy','pubring.gpg','trustdb.gpg']);
 const listing=execFileSync('tar',['-tzf',artifact],{encoding:'utf8'});
 for(const path of ['source/firmware-revisions.txt','source/qemu/roms/seabios/code.c','source/packages/mingw-w64-fixture-1.2.3-1.src.tar.zst','source/cage/patches/fixture.patch','source/SHA256SUMS'])assert.ok(listing.includes(path),path);
 assert.ok(!listing.includes('/.git'));
 assert.ok((await readFile(join(root,'verified'),'utf8')).includes('--verify'));
 const extract=join(root,'extracted');await mkdir(extract);execFileSync('tar',['-xzf',artifact,'-C',extract]);
 execFileSync('shasum',['-a','256','-c','SHA256SUMS'],{cwd:join(extract,'source'),stdio:'pipe'});
});
