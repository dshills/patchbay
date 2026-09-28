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
 const dir=fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(),'pb-browser-')));fs.chmodSync(dir,0o700);
 const socket=path.join(dir,'deckd.sock'),config=path.join(dir,'config.yaml'),recipeZip=path.join(dir,'benchmark.zip');
 let source=fs.readFileSync(path.join(root,'configs/benchmark.yaml'),'utf8').replaceAll('../bin/deckdemo',path.join(root,'bin/deckdemo')).replaceAll('../.cache/benchmark/',dir+'/').replace('path: ..}',`path: ${JSON.stringify(dir)}}`);
 fs.writeFileSync(config,source,{mode:0o600});
 const daemon=spawn(path.join(root,'bin/deckd'),['--config',config],{stdio:'ignore'});let helper,browser;
 try{
  await until(()=>fs.existsSync(socket));
  const testBinary=path.join(dir,'browser-helper');execFileSync('go',['test','-c','-o',testBinary,'./internal/workbench'],{cwd:root,stdio:'pipe'});
  helper=spawn(testBinary,['-test.run=^TestServeBrowser$'],{cwd:root,env:{...process.env,PATCHBAY_BROWSER_TEST_SOCKET:socket,PATCHBAY_BROWSER_TEST_RECIPE_OUT:recipeZip},stdio:['ignore','ignore','pipe','pipe']});
  const launch=await new Promise((resolve,reject)=>{let data='';const timer=setTimeout(()=>reject(Error('Browser helper did not start.')),10000);helper.stdio[3].on('data',chunk=>{data+=chunk;if(data.includes('\n')){clearTimeout(timer);resolve(data.trim());}});helper.once('exit',()=>{clearTimeout(timer);reject(Error('Browser helper exited.'));});});
  browser=await chromium.launch({headless:true});const context=await browser.newContext({acceptDownloads:true,viewport:{width:1280,height:1000}});let page;const errors=[];
  const renderTimes=[];const external=[];context.on('request',r=>{if(new URL(r.url()).origin!==new URL(launch).origin)external.push(new URL(r.url()).origin);});
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
  // Exchange a real portable recipe through the authenticated browser bridge.

  await page.locator('#recipes-open > summary').click();await page.locator('#recipe-file').setInputFiles(recipeZip);await page.locator('#recipe-upload').click();await page.locator('#recipe-editor').waitFor({state:'visible'});assert.equal(await page.evaluate(()=>window.recipeInjected===undefined),true);assert.equal(await page.locator('#recipe-documents script,#recipe-documents img').count(),0);
  await page.getByLabel('Local project for benchmark',{exact:true}).selectOption('benchmark');await page.getByLabel('Local tool for demo',{exact:true}).fill(path.join(root,'bin/deckdemo'));
  await page.locator('#recipe-prepare').click();await page.locator('#recipe-review').waitFor({state:'visible'});assert.equal(await page.locator('#recipe-commit').isDisabled(),true);
  const recipePreview=JSON.parse(await page.locator('#recipe-review-text').textContent());assert.equal(recipePreview.installation.active,true);assert.match(recipePreview.experiment_steps['recipe.'+recipePreview.installation.id+'.benchmark'][0].executable,/deckdemo$/);
  await page.locator('#recipe-confirm').focus();await page.keyboard.press('Space');await page.keyboard.press('Tab');assert.equal(await page.evaluate(()=>document.activeElement.id),'recipe-commit');await page.keyboard.press('Enter');await page.waitForFunction(()=>document.querySelector('#recipe-result').textContent.startsWith('activate committed'));
  await page.locator('#experiment').selectOption('recipe.'+recipePreview.installation.id+'.benchmark');await page.waitForFunction(()=>!document.querySelector('#prepare').disabled);
  await page.locator('#prepare').click();await page.locator('#confirm').check();await page.locator('#capture').click();await page.waitForFunction(()=>document.querySelector('#run-status').textContent.startsWith('success')&&document.querySelectorAll('.run-row').length===1);
  await page.getByText('Explore this recipe’s illustrative samples',{exact:true}).click();await page.locator('#recipe-compare-run').click();await page.waitForFunction(()=>document.querySelector('#comparison').textContent.includes('Illustrative sample comparison'));assert.doesNotMatch(await page.locator('#comparison').innerText(),/definitions differ/);
  await page.getByText('Share a portable ZIP',{exact:true}).click();await page.locator('#recipe-export-prepare').click();await page.locator('#recipe-export-review').waitFor({state:'visible'});const exportText=await page.locator('#recipe-export-files').textContent();assert.doesNotMatch(exportText,new RegExp(recipePreview.installation.id));assert.doesNotMatch(exportText,/private-note|PATCHBAY_OVERLAY/);
  await page.locator('#recipe-export-confirm').check();const recipeDownloadPromise=page.waitForEvent('download');await page.locator('#recipe-export-save').click();const recipeDownload=await recipeDownloadPromise;const portableZip=path.join(dir,'shared.zip');await recipeDownload.saveAs(portableZip);const inspected=JSON.parse(execFileSync(path.join(root,'bin/deckctl'),['recipe','inspect',portableZip,'--json'],{encoding:'utf8'}));assert.equal(inspected.manifest.id,'benchmark');assert.equal(inspected.samples.length,0);
  await page.locator('#recipe-operation').selectOption('deactivate');await page.waitForFunction(()=>!document.querySelector('#recipe-prepare').disabled);await page.locator('#recipe-prepare').click();await page.locator('#recipe-confirm').check();await page.locator('#recipe-commit').click();await page.waitForFunction(()=>document.querySelector('#recipe-result').textContent.startsWith('deactivate committed'));
  await page.waitForFunction(()=>[...document.querySelectorAll('#experiment option')].filter(o=>o.textContent.includes('Benchmark Playground')).length===1);
  assert.equal(await page.locator('#experiment option').filter({hasText:'Benchmark Playground'}).count(),1); // Host experiment remains available.
  await page.screenshot({path:path.join(root,'.cache/workbench-recipes.png'),fullPage:true});
  await page.screenshot({path:path.join(root,'.cache/workbench-main.png'),fullPage:true});
  // A second tab without the launch fragment cannot reuse the first tab's token.
  const other=await context.newPage();await other.goto(new URL(launch).origin);await other.waitForFunction(()=>document.querySelector('#notice').textContent.includes('Launch deckctl workbench'));assert.equal(await other.locator('#prepare').isDisabled(),true);
  await page.setViewportSize({width:390,height:844});await page.getByText('Explore illustrative samples',{exact:true}).click();await page.locator('#samples').click();await page.waitForFunction(()=>document.querySelector('#comparison').textContent.includes('Illustrative sample comparison'));
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
  await page.screenshot({path:path.join(root,'.cache/workbench-mobile.png'),fullPage:true});
  await stop(daemon);await page.waitForFunction(()=>document.querySelector('#prepare').disabled&&document.querySelector('#connection').textContent.includes('stale'));
  await page.screenshot({path:path.join(root,'.cache/workbench-disconnected.png'),fullPage:true});
  await page.reload();await page.waitForFunction(()=>document.querySelector('#notice').textContent.includes('Launch deckctl workbench'));assert.equal(await page.locator('#prepare').isDisabled(),true);assert.deepEqual(errors,[]);assert.deepEqual(external,[]);
 }finally{if(browser)await browser.close();await stop(helper);await stop(daemon);fs.rmSync(dir,{recursive:true,force:true});}
});

test('agent context review and generation use an isolated fake provider',{timeout:60000},async()=>{
 const dir=fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(),'pb-agent-browser-')));fs.chmodSync(dir,0o700);
 const config=path.join(dir,'config.yaml');fs.writeFileSync(path.join(dir,'selected.txt'),'Illustrative selected evidence.',{mode:0o600});fs.writeFileSync(config,JSON.stringify({version:1,server:{socket:path.join(dir,'socket')},state:{path:path.join(dir,'state')},context:{defaults:{project:'demo'}},projects:{demo:{name:'Demo',path:dir}},agents:{codex:{model:'fixture-model'},proposals:{enabled:true}}}),{mode:0o600});
 let helper,browser;
 try{
  const binary=path.join(dir,'helper');execFileSync('go',['test','-c','-o',binary,'./internal/workbench'],{cwd:root,stdio:'pipe'});
  helper=spawn(binary,['-test.run=^TestServeAgentBrowser$'],{cwd:root,env:{...process.env,PATCHBAY_BROWSER_AGENT_CONFIG:config},stdio:['ignore','ignore','pipe','pipe']});
  const launch=await new Promise((resolve,reject)=>{let data='';const timer=setTimeout(()=>reject(Error('Agent helper did not start.')),10000);helper.stdio[3].on('data',b=>{data+=b;if(data.includes('\n')){clearTimeout(timer);resolve(data.trim());}});helper.once('exit',()=>{clearTimeout(timer);reject(Error('Agent helper exited.'));});});
  browser=await chromium.launch({headless:true});const page=await browser.newPage({viewport:{width:1280,height:1000}});const errors=[],external=[];page.on('pageerror',e=>errors.push(e.message));page.on('request',r=>{if(new URL(r.url()).origin!==new URL(launch).origin)external.push(r.url());});
  await page.goto(launch);await page.waitForFunction(()=>document.querySelector('#connection').textContent==='Connected · local daemon');await page.locator('#agents-open > summary').click();await page.waitForFunction(()=>document.querySelector('#agent-availability').textContent.includes('Provider ready'));assert.equal(await page.locator('#agent-sessions button').count(),0);
  await page.locator('#agent-question').fill('Explain this evidence.');await page.locator('#agent-files').fill('selected.txt');await page.locator('#agent-context').click();await page.locator('#agent-consent').waitFor({state:'visible'});
  const preview=JSON.parse(await page.locator('#agent-context-text').textContent());assert.match(preview.input,/Illustrative selected evidence/);assert.equal(preview.destination,'https://api.openai.com/v1/responses');assert.equal(preview.retain_snapshots,false);assert.equal(await page.locator('#agent-start').isDisabled(),true);assert.equal(await page.locator('#agent-sessions button').count(),0);
  await page.locator('#agent-consent-check').focus();await page.keyboard.press('Space');await page.keyboard.press('Tab');assert.equal(await page.evaluate(()=>document.activeElement.id),'agent-start');await page.keyboard.press('Enter');await page.waitForFunction(()=>document.querySelector('#agent-session-state').textContent==='completed');
  assert.match(await page.locator('#agent-output').textContent(),/<script>/);assert.equal(await page.evaluate(()=>window.agentInjected),undefined);assert.equal(await page.locator('#agent-output script').count(),0);assert.match(await page.locator('#agent-sources').textContent(),/Unsupported model references/);assert.match(await page.locator('#agent-usage').textContent(),/Actual usage/);assert.equal(await page.locator('#agent-sessions button').count(),1);
  await page.screenshot({path:path.join(root,'.cache/workbench-agent-context.png'),fullPage:true});
  page.once('dialog',d=>d.accept());await page.locator('#agent-forget').click();await page.waitForFunction(()=>document.querySelectorAll('#agent-sessions button').length===0);assert.deepEqual(errors,[]);assert.deepEqual(external,[]);
 }finally{if(browser)await browser.close();await stop(helper);fs.rmSync(dir,{recursive:true,force:true});}
});
