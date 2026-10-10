import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import {readFileSync} from 'node:fs';
const html=readFileSync(new URL('../../web-redeem/index.html',import.meta.url),'utf8');
const source=html.match(/<script>([\s\S]*?)<\/script>/)[1];
class Element {
  constructor(){this.dataset={};this.value='';this.textContent='';this.children=[];this.style={};this.handlers={};this.disabled=false;this.hidden=false;this.className='';this.classList={add(){},remove(){},toggle(){}};}
  addEventListener(name,fn){this.handlers[name]=fn;}
  setAttribute(){}
  querySelectorAll(){return [];}
  querySelector(selector){return this.parts?.[selector]||new Element();}
  replaceChildren(){this.innerHTML='';this.children=[];}
  append(item){this.children.push(item);}
  focus(){}
  showModal(){this.open=true;}
  close(){this.open=false;this.handlers.close?.();}
}
function setup(){
  const nodes=new Map(), interval=[];
  const node=id=>{if(!nodes.has(id))nodes.set(id,new Element());return nodes.get(id);};
  const context=vm.createContext({document:{getElementById:node,querySelectorAll:()=>[],querySelector:()=>null,documentElement:{},createElement:()=>new Element(),body:new Element(),head:new Element()},localStorage:{getItem:()=>null,setItem(){}},window:{innerWidth:500},AbortController,Date,console,setTimeout,clearTimeout,setInterval:fn=>interval.push(fn),fetch:async()=>({ok:true,status:200,json:async()=>({turnstile_enabled:false})})});
  vm.runInContext(source,context);
  return {context,node,interval,run:code=>vm.runInContext(code,context)};
}
test('checks expire at five minutes and cannot transfer to another username',()=>{
  const s=setup();s.run("JOB=[{user:'alice',checkedUser:'alice',checkedAt:1000}]");
  assert.equal(s.run('checkFresh(JOB[0],300999)'),true);
  assert.equal(s.run('checkFresh(JOB[0],301000)'),false);
  s.run("JOB[0].user='bob'");assert.equal(s.run('checkFresh(JOB[0],1001)'),false);
});
test('stale eligibility response does not validate an edited account',async()=>{
  const s=setup(),row=new Element(),input=new Element(),button=new Element(),status=new Element();
  input.value='alice';row.parts={input,button,'.st':status};s.node('userFields').children=[row];
  s.run("JOB=[{user:'alice',revision:0,checkedAt:0,checkedUser:'',pending:false}]; request=()=>new Promise(resolve=>{window.resolveCheck=resolve;}); window.pendingCheck=verifyAccount(0);");
  input.value='bob';s.run("JOB[0].user='bob';JOB[0].revision++;JOB[0].pending=false;window.resolveCheck({results:[{eligible:true}]});");
  await s.context.window.pendingCheck;
  assert.equal(s.run('JOB[0].checkedAt'),0);assert.equal(s.run('JOB[0].checkedUser'),'');
});
test('busy verification remains unverified, without classifying the account invalid',async()=>{
  const s=setup(),row=new Element(),input=new Element(),button=new Element(),status=new Element();
  input.value='demo_busy';row.parts={input,button,'.st':status};s.node('userFields').children=[row];
  s.run("JOB=[{user:'demo_busy',revision:0,checkedAt:0,checkedUser:'',pending:false}];request=async()=>({results:[{eligible:false,retry:true}]});");
  await s.run('verifyAccount(0)');assert.equal(s.run('JOB[0].checkedAt'),0);assert.equal(row.className,'urow');assert.match(status.textContent,/繁忙/);
});
test('opening a different code discards old lookup result and invalidates pending responses',()=>{
  const s=setup();s.node('lookupCode').value='XPP-DEMOREDEEMED0001';s.node('lookupResult').innerHTML='Old result';
  s.run("lastLookup={code:'XPP-DEMOREDEEMED0001',status:'succeeded'};lookupSerial=7;openLookup('XPP-DEMOACTIVE000001');");
  assert.equal(s.node('lookupCode').value,'XPP-DEMOACTIVE000001');assert.equal(s.run('lastLookup'),null);assert.equal(s.run('lookupSerial'),8);assert.equal(s.node('lookupResult').innerHTML,'');
});
test('lookup input edit prevents a pending response from showing results for the old code',async()=>{
  const s=setup();s.node('lookupCode').value='XPP-DEMOREDEEMED0001';
  s.run("humanToken=async()=>'';request=()=>new Promise(resolve=>{window.resolveLookup=resolve;});");
  const submitted=s.node('lookupForm').handlers.submit({preventDefault(){}});
  await Promise.resolve();await Promise.resolve();
  s.node('lookupCode').value='XPP-DEMOACTIVE000001';s.node('lookupCode').handlers.input();
  s.context.window.resolveLookup({status:'succeeded',months:3,progress:100});await submitted;
  assert.equal(s.run('lastLookup'),null);assert.equal(s.node('lookupResult').innerHTML,'');
});
test('server outage and missing code remain distinct and HTML in results is escaped',()=>{
  const s=setup();assert.match(s.run("errorPair(new APIError('',503))[1]"),/unconfirmed/);assert.match(s.run("errorPair(new APIError('',404))[1]"),/not found/);
  s.run("renderLookup({code:'XPP-DEMOREDEEMED0001',status:'succeeded',months:3,username_masked:'<img src=x onerror=alert(1)>',progress:100});");
  assert.ok(!s.node('lookupResult').innerHTML.includes('<img'));assert.match(s.node('lookupResult').innerHTML,/&lt;img/);
});
test('an expired check cannot submit or call the redemption endpoint',async()=>{
  const s=setup(),row=new Element(),input=new Element(),button=new Element();input.value='alice';row.parts={input,button};s.node('userFields').children=[row];
  s.run("JOB=[{user:'alice',checkedUser:'alice',checkedAt:Date.now()-300001,pending:false}];request=()=>{throw new Error('Redemption endpoint must not be called');};");
  await s.node('btn2').handlers.click();assert.match(s.node('notice2').textContent,/5 分钟/);assert.equal(s.run('submitting'),false);
});
