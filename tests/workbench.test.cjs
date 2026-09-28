'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const os=require('node:os');
const path=require('node:path');
const {spawn,execFileSync}=require('node:child_process');
const {chromium}=require('playwright');
const root=path.resolve(__dirname,'..');
const delay=ms=>new Promise(r=>setTimeout(r,ms));
async function until(predicate,timeout=10000){const deadline=Date.now()+timeout;while(!predicate()){if(Date.now()>deadline)throw Error('Fixture did not become ready.');await delay(50);}}
async function focusByKeyboard(page,id){for(let i=0;i<50;i++){await page.keyboard.press('Tab');if(await page.evaluate(()=>document.activeElement?.id)===id)return;}throw Error('Keyboard could not reach '+id);}
async function stop(child){if(!child||child.exitCode!==null)return;child.kill('SIGTERM');await Promise.race([new Promise(resolve=>child.once('exit',resolve)),delay(3000)]);if(child.exitCode===null)child.kill('SIGKILL');}

test('real workbench: capture, baseline, comparison, export, token loss, and stale state',{timeout:90000},async()=>{
 const dir=fs.mkdtempSync(path.join(os.tmpdir(),'pb-browser-'));fs.chmodSync(dir,0o700);
 const socket=path.join(dir,'deckd.sock'),config=path.join(dir,'config.yaml');
 let source=fs.readFileSync(path.join(root,'configs/benchmark.yaml'),'utf8').replaceAll('../bin/deckdemo',path.join(root,'bin/deckdemo')).replaceAll('../.cache/benchmark/',dir+'/').replace('path: ..}',`path: ${JSON.stringify(dir)}}`);
 fs.writeFileSync(config,source,{mode:0o600});
 const daemon=spawn(path.join(root,'bin/deckd'),['--config',config],{stdio:'ignore'});let helper,browser;
 try{
  await until(()=>fs.existsSync(socket));
  const testBinary=path.join(dir,'browser-helper');execFileSync('go',['test','-c','-o',testBinary,'./internal/workbench'],{cwd:root,stdio:'pipe'});
  helper=spawn(testBinary,['-test.run=^TestServeBrowser$'],{cwd:root,env:{...process.env,PATCHBAY_BROWSER_TEST_SOCKET:socket},stdio:['ignore','ignore','pipe','pipe']});
  const launch=await new Promise((resolve,reject)=>{let data='';const timer=setTimeout(()=>reject(Error('Browser helper did not start.')),10000);helper.stdio[3].on('data',chunk=>{data+=chunk;if(data.includes('\n')){clearTimeout(timer);resolve(data.trim());}});helper.once('exit',()=>{clearTimeout(timer);reject(Error('Browser helper exited.'));});});
  browser=await chromium.launch({headless:true});const context=await browser.newContext({acceptDownloads:true,viewport:{width:1280,height:1000}});let page;const errors=[];
  const renderTimes=[];
  for(let sample=0;sample<10;sample++){if(page)await page.close();page=await context.newPage();page.on('pageerror',error=>errors.push(error.message));const began=performance.now();await page.goto(launch);await page.waitForFunction(()=>document.querySelector('#connection').textContent==='Connected · local daemon');renderTimes.push(performance.now()-began);}
  renderTimes.sort((a,b)=>a-b);console.log(JSON.stringify({first_render_samples:10,median_ms:renderTimes[5],p95_ms:renderTimes[9],platform:process.platform,arch:process.arch}));
  assert.equal(new URL(page.url()).hash==='',true,'Launch fragment must be erased.');assert.equal(await page.evaluate(()=>localStorage.length),0);assert.equal(await page.locator('.run-row').count(),0);
  await focusByKeyboard(page,'prepare');await page.keyboard.press('Enter');await page.locator('#preview').waitFor({state:'visible'});await focusByKeyboard(page,'confirm');await page.keyboard.press('Space');await focusByKeyboard(page,'capture');await page.keyboard.press('Enter');await page.waitForFunction(()=>document.querySelector('#run-status').textContent.startsWith('success'));
  await page.locator('#baseline').click();await page.waitForFunction(()=>document.querySelector('#baseline-label').textContent.startsWith('Baseline:'));
  await page.locator('#param-iterations').fill('20000');await page.locator('#param-iterations').press('Tab');await page.waitForFunction(()=>!document.querySelector('#prepare').disabled);
  await page.locator('#prepare').click();await page.locator('#confirm').check();await page.locator('#capture').click();await page.waitForFunction(()=>document.querySelectorAll('.run-row').length===2&&document.querySelector('#run-status').textContent.startsWith('success'));
  await page.locator('#compare').click();await page.waitForFunction(()=>document.querySelector('#comparison').textContent.includes('duration'));assert.match(await page.locator('#comparison').innerText(),/duration/);
  await page.locator('#note').fill('<script>private-note</script>');await page.locator('#save-note').click();await page.waitForFunction(()=>!document.querySelector('#export-preview').disabled);
  await page.locator('#export-preview').click();await page.locator('#export-review').waitFor({state:'visible'});assert.doesNotMatch(await page.locator('#export-text').innerText(),/private-note|environment_names|executable/);
  const downloadPromise=page.waitForEvent('download');await page.locator('#download-html').click();const download=await downloadPromise;const file=path.join(dir,'report.html');await download.saveAs(file);const html=fs.readFileSync(file,'utf8');assert.match(html,/Patchbay experiment report/);assert.doesNotMatch(html,/<script|private-note/);
  await page.screenshot({path:path.join(root,'.cache/workbench-main.png'),fullPage:true});
  // A second tab without the launch fragment cannot reuse the first tab's token.
  const other=await context.newPage();await other.goto(new URL(launch).origin);await other.waitForFunction(()=>document.querySelector('#notice').textContent.includes('Launch deckctl workbench'));assert.equal(await other.locator('#prepare').isDisabled(),true);
  await page.setViewportSize({width:390,height:844});await page.getByText('Explore illustrative samples',{exact:true}).click();await page.locator('#samples').click();await page.waitForFunction(()=>document.querySelector('#comparison').textContent.includes('Illustrative sample comparison'));
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
  await page.screenshot({path:path.join(root,'.cache/workbench-mobile.png'),fullPage:true});
  await stop(daemon);await page.waitForFunction(()=>document.querySelector('#prepare').disabled&&document.querySelector('#connection').textContent.includes('stale'));
  await page.screenshot({path:path.join(root,'.cache/workbench-disconnected.png'),fullPage:true});
  await page.reload();await page.waitForFunction(()=>document.querySelector('#notice').textContent.includes('Launch deckctl workbench'));assert.equal(await page.locator('#prepare').isDisabled(),true);assert.deepEqual(errors,[]);
 }finally{if(browser)await browser.close();await stop(helper);await stop(daemon);fs.rmSync(dir,{recursive:true,force:true});}
});
