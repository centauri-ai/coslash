import {mkdtempSync,mkdirSync,writeFileSync,readFileSync} from 'node:fs';
import {spawnSync,execFileSync} from 'node:child_process';
import {tmpdir} from 'node:os';
import {fileURLToPath} from 'node:url';
import path from 'node:path';
import assert from 'node:assert/strict';
const release=mkdtempSync(path.join(tmpdir(),'coslash-pi-cleanup-check-'));
const cli=path.join(release,'node_modules/@earendil-works/pi-coding-agent/dist/bundle/cli.js');
const pidFile=path.join(release,'owned-pids');mkdirSync(path.dirname(cli),{recursive:true});
writeFileSync(cli,`const fs=require('node:fs'),path=require('node:path');
const dir=path.join(process.env.COSLASH_HOME,'pi-runtime');fs.mkdirSync(dir,{recursive:true});fs.appendFileSync(process.env.PI_TEST_CHILD_PIDS,process.pid+'\\n');
if(process.argv.includes('--session')) {process.stdin.resume();setInterval(()=>{},1000);}else{
 const r={runtimeId:String(process.pid),pid:process.pid,sessionId:'first',processStartIdentity:'ps:test',workState:'idle',sequence:1,transcriptPath:path.join(process.argv[process.argv.indexOf('--session-dir')+1],'test.jsonl')};
 const save=()=>fs.writeFileSync(path.join(dir,process.pid+'.json'),JSON.stringify(r));save();
 process.stdin.on('data',data=>{for(const line of data.toString().trim().split('\\n')){const v=JSON.parse(line);
 if(v.type==='prompt'&&v.message==='/probe-wait'){r.dialogOpen=true;console.log(JSON.stringify({type:'extension_ui_request',method:'confirm',id:'q'}));save();}
 else if(v.type==='extension_ui_response'){r.dialogOpen=false;save();}
 else if(v.type==='prompt'||v.type==='compact'){r.workState='busy';r.sequence++;save();setTimeout(()=>{r.workState='idle';r.sequence++;save();if(v.type==='compact')console.log(JSON.stringify({type:'response',command:'compact',success:true}));},300);}
 }});
}
`);
const result=spawnSync(process.execPath,[fileURLToPath(new URL('./runtime-check.mjs',import.meta.url))],{env:{...process.env,PI_TEST_RELEASE_DIR:release,PI_TEST_CHILD_PIDS:pidFile},encoding:'utf8',timeout:30000});
const pids=readFileSync(pidFile,'utf8').trim().split('\n').map(Number);
const alive=pid=>{try{process.kill(pid,0);return true}catch{return false}};
try{
 assert.equal(result.status,1,'native check must report duplicate-owner timeout');assert.match(result.stderr,/timeout:/);assert.equal(pids.length,2);
 for(let i=0;i<20&&pids.some(alive);i++)await new Promise(r=>setTimeout(r,50));
 assert.deepEqual(pids.map(alive),[false,false],'every spawned runtime must exit on duplicate-owner timeout');
 console.log('PASS failure cleanup: actual native check times out, both owned child processes exit.');
}finally{
 for(const pid of pids)if(alive(pid)){
  const command=execFileSync('ps',['-p',String(pid),'-o','command='],{encoding:'utf8'});
  assert.ok(command.includes(cli),'refuse to terminate a process outside the isolated fake release');
  process.kill(pid,'SIGKILL');
 }
}
