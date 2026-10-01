// Manual native release compatibility probe, excluded from make test and CI.
import { spawn } from 'node:child_process';
import { mkdtempSync, mkdirSync, copyFileSync, readdirSync, readFileSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import assert from 'node:assert/strict';
const root=mkdtempSync(path.join(tmpdir(),'coslash-pi-production-'));
const agent=path.join(root,'agent');mkdirSync(path.join(agent,'extensions'),{recursive:true});
copyFileSync(new URL('../coslash-extension.ts',import.meta.url),path.join(agent,'extensions','coslash-extension.ts'));
writeFileSync(path.join(agent,'settings.json'),JSON.stringify({compaction:{enabled:false,reserveTokens:512,keepRecentTokens:1}}));
const env={...process.env,PI_CODING_AGENT_DIR:agent,COSLASH_HOME:path.join(root,'coslash'),XDG_CONFIG_HOME:path.join(root,'xdg-config'),XDG_DATA_HOME:path.join(root,'xdg-data')};
const release=process.env.PI_TEST_RELEASE_DIR;
assert.ok(release,'Set PI_TEST_RELEASE_DIR to an installed verified Pi release directory containing node_modules');
const cli=path.join(release,'node_modules/@earendil-works/pi-coding-agent/dist/bundle/cli.js');
const provider=path.join(root,'offline-provider.ts');
writeFileSync(provider,readFileSync(new URL('./offline-provider.ts',import.meta.url),'utf8').replace('__PI_AI_EVENT_STREAM__',path.join(release,'node_modules/@earendil-works/pi-ai/dist/utils/event-stream.js')));
const sleep=ms=>new Promise(resolve=>setTimeout(resolve,ms));
const claims=()=>{try{return readdirSync(path.join(env.COSLASH_HOME,'pi-runtime')).filter(x=>x.endsWith('.json')).map(x=>JSON.parse(readFileSync(path.join(env.COSLASH_HOME,'pi-runtime',x),'utf8')))}catch{return []}};
async function until(predicate){for(let i=0;i<120;i++){if(predicate())return;await sleep(100)}throw Error('timeout: '+JSON.stringify(claims()))}
function start(args=[]){const child=spawn(process.execPath,[cli,'--mode','rpc','--no-tools','-e',provider,'--provider','coslash-probe','--model','probe',...args],{cwd:root,env});let output='';child.stdout.on('data',d=>output+=d);child.stderr.on('data',d=>output+=d);child.output=()=>output;child.send=(v)=>child.stdin.write(JSON.stringify(v)+'\n');return child;}
const p=start(['--session-dir',path.join(root,'override')]);
let second;
try{
 await until(()=>claims().length===1);const first=claims()[0];assert.equal(first.workState,'idle');assert.equal(first.entrypoint,'pi-rpc');assert.match(first.processStartIdentity,/^(ps|linux):/);
 p.send({type:'prompt',message:'hello'});await until(()=>claims()[0]?.workState==='busy');
 await until(()=>claims()[0]?.workState==='idle');assert.ok(claims()[0].sequence>first.sequence);
 p.send({type:'prompt',message:'abort'});await until(()=>claims()[0]?.workState==='busy');p.send({type:'abort'});await until(()=>claims()[0]?.workState==='idle');
 p.send({type:'prompt',message:'/probe-wait'});await until(()=>claims()[0]?.dialogOpen===true);assert.equal(claims()[0].workState,'idle');
 const wait=p.output().split('\n').map(line=>{try{return JSON.parse(line)}catch{return null}}).find(x=>x?.type==='extension_ui_request'&&x.method==='confirm');assert.ok(wait);p.send({type:'extension_ui_response',id:wait.id,confirmed:true});await until(()=>claims()[0]?.dialogOpen===false);
 p.send({type:'compact'});await until(()=>claims()[0]?.workState==='busy');await until(()=>claims()[0]?.workState==='idle');
 await until(()=>p.output().split('\n').some(line=>{try{const x=JSON.parse(line);return x.type==='response'&&x.command==='compact'&&x.success===true}catch{return false}}));
 const session=claims()[0].transcriptPath;assert.ok(session.startsWith(path.join(root,'override')));
 second=start(['--session',session]);await until(()=>claims().length===2);assert.notEqual(claims()[0].runtimeId,claims()[1].runtimeId);
 second.kill('SIGKILL');await new Promise(resolve=>second.once('exit',resolve));assert.equal(claims().length,2);
 p.send({type:'new_session'});await until(()=>claims().some(x=>x.pid===p.pid&&x.sessionId!==first.sessionId));
 const switchedHistory=readdirSync(path.join(env.COSLASH_HOME,'pi-history')).map(x=>JSON.parse(readFileSync(path.join(env.COSLASH_HOME,'pi-history',x),'utf8')));
 assert.ok(switchedHistory.some(x=>x.exited&&x.record.transcriptPath===session),'previous session must be retired before process exits');
 p.send({type:'prompt',message:'replacement session'});await until(()=>claims().some(x=>x.pid===p.pid&&x.workState==='busy'));await until(()=>claims().some(x=>x.pid===p.pid&&x.workState==='idle'));
 const replacement=claims().find(x=>x.pid===p.pid).transcriptPath;
 p.send({type:'switch_session',sessionPath:session});await until(()=>claims().some(x=>x.pid===p.pid&&x.transcriptPath===session));
 const switchedBackHistory=readdirSync(path.join(env.COSLASH_HOME,'pi-history')).map(x=>JSON.parse(readFileSync(path.join(env.COSLASH_HOME,'pi-history',x),'utf8')));
 assert.ok(switchedBackHistory.some(x=>x.exited&&x.record.transcriptPath===replacement),'switch_session must retire previous history before process exits');
 p.stdin.end();await new Promise(resolve=>p.once('exit',resolve));assert.equal(claims().filter(x=>x.pid===p.pid).length,0);
 const histories=readdirSync(path.join(env.COSLASH_HOME,'pi-history')).map(x=>JSON.parse(readFileSync(path.join(env.COSLASH_HOME,'pi-history',x),'utf8')));
 assert.ok(histories.some(x=>x.exited&&x.record.transcriptPath===session));
 console.log('PASS production runtime: idle, busy, settled, abort, duplicate owners, hard kill retained, session replacement, graceful EOF retained history');
 console.log('isolated root: '+root);
}catch(error){console.error(p.output());throw error}finally{for(const child of [p,second])child?.kill('SIGKILL')}
