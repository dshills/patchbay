'use strict';
// Erase the fragment before requests; the token never enters persistent storage.
let token = location.hash.slice(1);
history.replaceState(null, '', location.pathname);
const $ = id => document.getElementById(id);
const state = {lastGood:0, busy:false, syncing:false, resync:true, key:'', data:null, experiment:'', preview:null, selected:null, baseline:null, active:null, export:null, cursor:'', extra:[], closed:false, owned:false};
const terminal = s => ['success','failed','cancelled','interrupted','recording_failed'].includes(s);
function notice(message='') {$('notice').textContent=message;$('notice').hidden=!message;}
function text(tag,value,className=''){const e=document.createElement(tag);e.textContent=value;e.className=className;return e;}
function unsafeNumber(v){if(typeof v==='number')return !Number.isFinite(v)||(Number.isInteger(v)&&!Number.isSafeInteger(v));if(Array.isArray(v))return v.some(unsafeNumber);return v&&typeof v==='object'&&Object.values(v).some(unsafeNumber);}
async function api(path,method='GET',body){
 const controller=new AbortController();const timer=setTimeout(()=>controller.abort(),5000);
 try{const response=await fetch('/api/'+path,{method,headers:{Authorization:'Bearer '+token,...(method==='GET'?{}:{'Content-Type':'application/json'})},body:body===undefined?undefined:JSON.stringify(body),signal:controller.signal,cache:'no-store',credentials:'omit',redirect:'error'});const value=await response.json();if(!response.ok){const error=new Error(value.error?.message||'Request failed.');error.code=value.error?.code;throw error;}if(unsafeNumber(value))throw new Error('This result contains numbers beyond the browser’s exact range. Inspect it with deckctl.');return value;}finally{clearTimeout(timer);}
}
function live(){return !state.closed&&Date.now()-state.lastGood<2000&&!!state.data;}
function buttons(){
 const ready=live()&&!state.busy;
 document.querySelectorAll('.mutation').forEach(e=>{e.disabled=!ready;});
 $('project').disabled=!ready;$('experiment').disabled=!ready;
 document.querySelectorAll('#parameters input,#parameters select').forEach(e=>{e.disabled=!ready;});
 $('prepare').disabled=!ready||!state.experiment||!state.data?.storage.available||state.data?.storage.read_only;
 $('capture').disabled=!ready||!state.preview||Date.now()>=Date.parse(state.preview.expires_at)||!$('confirm').checked;
 $('compare').disabled=!ready||!state.selected||!state.baseline?.run_id||state.baseline.run_id===state.selected.id;
 $('baseline').disabled=!ready||state.selected?.state!=='success';
 $('delete').disabled=!ready||!state.selected||!terminal(state.selected.state)||state.selected.annotation.pinned;
 $('save-note').disabled=!ready||!state.selected;
 $('cancel').disabled=!ready||!state.active;
 $('export-preview').disabled=!ready||!state.selected||!terminal(state.selected.state);
 $('download-html').disabled=!ready||!state.export||Date.now()>=Date.parse(state.export.expires_at);$('download-json').disabled=$('download-html').disabled;
 $('connection').textContent=state.closed?'Session closed':live()?'Connected · local daemon':'Disconnected or stale · controls paused';
 if(state.preview)$('expiry').textContent=Date.now()>=Date.parse(state.preview.expires_at)?'Approval expired. Review capture again to refresh it.':'Approval expires at '+new Date(state.preview.expires_at).toLocaleTimeString();
}
function clearPreview(){state.preview=null;$('preview').hidden=true;$('confirm').checked=false;}
function clearExport(){state.export=null;$('export-review').hidden=true;}
async function mutation(fn){if(!live()||state.busy)return;state.busy=true;buttons();notice();try{await fn();}catch(error){notice(error.message);if(error.code==='stale_preparation')clearPreview();}finally{state.busy=false;await refresh();buttons();}}
function options(element,items,value){const stamp=JSON.stringify(items);if(element.dataset.items!==stamp){element.replaceChildren(...items.map(item=>{const e=document.createElement('option');e.value=item.id;e.textContent=item.title;return e;}));element.dataset.items=stamp;}element.value=value;}
function experiment(){return state.data?.experiments.experiments.find(e=>e.id===state.experiment);}
function parameters(){
 const e=experiment();$('description').textContent=e?.description||'Choose an experiment to begin.';
 const names=[...new Set([...(e?.inputs||[]).map(i=>i.parameter),...(e?.parameters||[])])];const definitions=names.map(name=>state.data.parameters.parameters.find(p=>p.name===name)).filter(Boolean);
 const signature=JSON.stringify(definitions.map(p=>({...p,value:undefined,synchronization:undefined})))+state.experiment;
 if($('parameters').dataset.signature!==signature){
  $('parameters').replaceChildren();$('parameters').dataset.signature=signature;
  for(const p of definitions){const id='param-'+p.name;const label=text('label',p.name+(p.unit?' · '+p.unit:''));label.htmlFor=id;let input;
   if(p.type==='enum'){input=document.createElement('select');for(const item of p.enum||[]){const option=text('option',item);option.value=item;input.append(option);}}
   else{input=document.createElement('input');input.type=p.type==='boolean'?'checkbox':['integer','float'].includes(p.type)?'number':'text';if(p.min!==undefined)input.min=p.min;if(p.max!==undefined)input.max=p.max;input.step=p.step??(p.type==='integer'?1:'any');}
   input.id=id;input.dataset.name=p.name;input.setAttribute('aria-label',label.textContent);
   input.addEventListener('change',()=>mutation(async()=>{let value=p.type==='boolean'?input.checked:input.value;if(p.type==='integer'||p.type==='float'){value=Number(value);if(!Number.isFinite(value)||(p.type==='integer'&&!Number.isSafeInteger(value)))throw new Error('Enter a finite value within the declared bounds.');}await api('parameters/'+encodeURIComponent(p.name),'PUT',{value});clearPreview();}));
   $('parameters').append(label,input);if(p.instrument){const readback=text('p','','muted');readback.id='observed-'+p.name;$('parameters').append(readback);}
  }
 }
 for(const p of definitions){const observation=$('observed-'+p.name);if(observation){const sync=p.synchronization;observation.textContent='Desired setting only. Readback: '+(sync?.observed??'unknown')+' '+(p.unit||'')+' · '+(sync?.status||'unknown')+'. Apply and output enable are separate actions.';}const input=$('param-'+p.name);if(!input||document.activeElement===input)continue;if(p.type==='boolean')input.checked=!!p.value;else input.value=p.value;}
}
function renderHistory(){
 const entries=[...state.data.runs.runs,...state.extra].filter((r,i,a)=>a.findIndex(x=>x.id===r.id)===i);const signature=JSON.stringify(entries);
 if($('history').dataset.signature===signature)return;$('history').dataset.signature=signature;$('history').replaceChildren();
 if(!entries.length)$('history').append(text('p','No saved runs yet. Review a capture or explore the illustrative samples below.','muted'));
 for(const run of entries){const row=document.createElement('div');row.className='run-row';const button=text('button',(run.annotation.title||run.experiment)+' · '+new Date(run.created_at).toLocaleTimeString());button.addEventListener('click',()=>selectRun(run.id).catch(e=>notice(e.message)));row.append(button,text('span',run.state,'badge'));$('history').append(row);}
 $('more').hidden=!state.cursor;
}
function table(headers,rows){const table=document.createElement('table');const head=document.createElement('thead');const tr=document.createElement('tr');headers.forEach(h=>tr.append(text('th',h)));head.append(tr);const body=document.createElement('tbody');for(const row of rows){const tr=document.createElement('tr');row.forEach(cell=>tr.append(text('td',cell===null||cell===undefined?'Unavailable':typeof cell==='number'?new Intl.NumberFormat(undefined,{maximumSignificantDigits:7}).format(cell):String(cell))));body.append(tr);}table.append(head,body);const wrapper=document.createElement('div');wrapper.className='table-wrap';wrapper.tabIndex=0;wrapper.setAttribute('role','region');wrapper.setAttribute('aria-label','Data table: '+headers.join(', '));wrapper.append(table);return wrapper;}
function chart(series){
 const section=document.createElement('section');section.append(text('h3',series.name),text('p',`${series.quality} · X: ${series.x_unit} · Y: ${series.y_unit}`,'muted'));
 const canvas=document.createElement('canvas');canvas.width=800;canvas.height=240;canvas.className='chart';canvas.setAttribute('role','img');canvas.setAttribute('aria-label',series.name+' waveform; complete values in the table below.');
 const context=canvas.getContext('2d');const minY=Math.min(...series.y),maxY=Math.max(...series.y),minX=series.x[0],maxX=series.x.at(-1);context.strokeStyle='#16684f';context.lineWidth=2;context.beginPath();series.x.forEach((x,i)=>{const px=20+(x-minX)/(maxX-minX||1)*760,py=220-(series.y[i]-minY)/(maxY-minY||1)*200;i?context.lineTo(px,py):context.moveTo(px,py);});context.stroke();section.append(canvas);
 const details=document.createElement('details');details.append(text('summary','Show all '+series.x.length+' data points'));details.addEventListener('toggle',()=>{if(details.open&&!details.dataset.loaded){details.dataset.loaded='true';details.append(table(['X ('+series.x_unit+')','Y ('+series.y_unit+')'],series.x.map((x,i)=>[x,series.y[i]])));}});section.append(details);return section;
}
async function selectRun(id,update=false){
 const run=await api('runs/'+encodeURIComponent(id));if(update&&state.selected?.id!==id){if(terminal(run.state))state.active=null;return;}state.selected=run;$('detail').hidden=false;
 $('run-title').textContent=run.annotation.title||run.experiment.title;$('run-status').textContent=run.state+(run.error?' · '+run.error.message:'')+(run.source_changed?' · Source changed during capture.':'');
 const observed=new Map();for(const outcome of run.outcomes||[]){const o=outcome.instrument;if(!o?.values)continue;const key=o.device+':'+o.channel;const serialized=JSON.stringify(o.values);if(observed.has(key)&&observed.get(key)!==serialized)$('run-status').textContent+=' · Instrument readback changed during capture.';observed.set(key,serialized);}
 $('metrics').replaceChildren(table(['Measurement','Value','Unit','Repeats / range','Status'],run.measurements.map(m=>[m.name,m.value,m.unit,m.repeats?m.repeats+' / '+(m.spread===undefined?'unavailable':Number(m.spread.toPrecision(6))):'—',m.status+(m.reason?' · '+m.reason:'')])));
 if(!update){$('annotation-title').value=run.annotation.title;$('note').value=run.annotation.note;$('pinned').checked=run.annotation.pinned;clearExport();}
 const seriesSignature=JSON.stringify([run.id,run.artifacts,run.outcomes]);if($('series').dataset.signature!==seriesSignature){$('series').dataset.signature=seriesSignature;$('series').replaceChildren();for(const outcome of run.outcomes||[]){if(outcome.instrument){const details=document.createElement('details');details.append(text('summary','Instrument observation · step '+outcome.index),text('pre',JSON.stringify(outcome.instrument,null,2)));$('series').append(details);}}
  for(const artifact of run.artifacts){if(artifact.media_type!=='application/json')continue;const value=await api('runs/'+encodeURIComponent(id)+'/artifacts/'+encodeURIComponent(artifact.id));if(state.selected?.id!==id)return;try{const data=JSON.parse(new TextDecoder().decode(Uint8Array.from(atob(value.data_base64),c=>c.charCodeAt(0))));if(data.schema_version===1&&Array.isArray(data.x)&&data.x.length<=10000)$('series').append(chart(data));}catch(error){$('series').append(text('p','Series could not be displayed: '+error.message));}}
 }
 if(!terminal(run.state))state.active={run_id:run.id,job_id:run.job_id};else if(state.active?.run_id===run.id)state.active=null;
 $('active').textContent=state.active?'Capture '+state.active.run_id+' is active.':'';$('cancel').hidden=!state.active;buttons();
}
async function baseline(){if(!state.experiment){state.baseline=null;return;}state.baseline=await api('baselines/'+encodeURIComponent(state.experiment)+'?project='+encodeURIComponent(state.data.context.project||''));$('baseline-label').textContent=state.baseline.run_id?'Baseline: '+state.baseline.run_id:'Select a successful run and choose “Use as baseline”.';}
async function refresh(){
 if(state.syncing||state.closed||!token)return;state.syncing=true;
 try{
  const [context,projects,experiments,parameters,runs,storage,status,capabilities,session]=await Promise.all(['context','projects','experiments','parameters','runs','storage','status','capabilities','session'].map(path=>api(path)));
  state.owned=session.owned_daemon;$('quit').textContent=state.owned?'Quit demo':'Close session';document.querySelector('footer').textContent=state.owned?'Closing this tab leaves jobs running. Quit demo stops this session’s daemon and cancels active jobs. Saved results remain available.':'Closing this tab leaves daemon jobs running. Close session revokes this browser session.';
  if(capabilities.features.capture!==1)throw new Error('This daemon does not support the workbench. Start a compatible Patchbay release.');
  const key=JSON.stringify([status.instance,status.generation,context,parameters]);if(state.resync||(state.key&&key!==state.key)){clearPreview();state.extra=[];state.cursor=runs.next_cursor||'';}state.key=key;
  if(state.data&&state.data.context.project!==context.project){state.selected=null;$('detail').hidden=true;clearExport();}
  state.data={context,projects,experiments,parameters,runs,storage,status};
  if(!experiments.experiments.some(e=>e.id===state.experiment)){state.experiment=experiments.experiments[0]?.id||'';clearPreview();}
  options($('project'),[{id:'',title:'No project'},...projects.projects.map(p=>({id:p.id,title:p.name}))],context.project||'');options($('experiment'),experiments.experiments.map(e=>({id:e.id,title:e.title})),state.experiment);
  state.data.runs=await api('runs?project='+encodeURIComponent(context.project||'')+'&experiment='+encodeURIComponent(state.experiment));parametersUI();if(!state.extra.length)state.cursor=state.data.runs.next_cursor||'';renderHistory();await baseline();
  $('storage').textContent=storage.available?`${storage.runs}/${storage.max_runs} runs · ${(storage.bytes/1048576).toFixed(1)} MiB saved`:'Storage unavailable';if(storage.read_only)$('storage').textContent+=' · Read only: '+storage.diagnostics.join(' ');
  if(state.active){await selectRun(state.active.run_id,true);}$('active').textContent=state.active?'Capture '+state.active.run_id+' is active.':'';$('cancel').hidden=!state.active;
  state.lastGood=Date.now();state.resync=false;
 }catch(error){state.resync=true;notice(error.message);}finally{state.syncing=false;buttons();}
}
const parametersUI=parameters;
$('project').addEventListener('change',()=>mutation(async()=>{await api('context/project','PUT',{project:$('project').value});state.selected=null;state.experiment='';$('detail').hidden=true;clearPreview();clearExport();}));
$('experiment').addEventListener('change',()=>{state.experiment=$('experiment').value;state.selected=null;state.extra=[];$('detail').hidden=true;clearPreview();clearExport();refresh();});
$('prepare').addEventListener('click',()=>mutation(async()=>{state.preview=await api('captures/prepare','POST',{experiment:state.experiment});$('preview-text').textContent=JSON.stringify(state.preview,null,2);$('preview').hidden=false;$('confirm').checked=false;}));
$('confirm').addEventListener('change',buttons);
$('capture').addEventListener('click',()=>mutation(async()=>{if(!state.preview||!$('confirm').checked)return;const request={preparation:state.preview.id,digest:state.preview.digest,request_id:Date.now()+'-'+crypto.randomUUID().replaceAll('-',''),confirmed:true};try{const response=await api('captures','POST',request);state.active=response;clearPreview();await selectRun(response.run_id);}catch(error){if(!error.code)error.message+=' Admission may have succeeded. Inspect saved runs; no retry was sent. Request ID: '+request.request_id;throw error;}}));
$('cancel').addEventListener('click',()=>mutation(async()=>{if(state.active)await api('jobs/'+encodeURIComponent(state.active.job_id),'DELETE');}));
$('baseline').addEventListener('click',()=>mutation(async()=>{await api('baselines/'+encodeURIComponent(state.selected.experiment.id)+'?project='+encodeURIComponent(state.selected.project),'PUT',{run_id:state.selected.id,revision:state.baseline?.revision||0});clearPreview();}));
$('save-note').addEventListener('click',()=>mutation(async()=>{await api('runs/'+encodeURIComponent(state.selected.id)+'/annotation','PUT',{revision:state.selected.annotation.revision,title:$('annotation-title').value,note:$('note').value,pinned:$('pinned').checked});await selectRun(state.selected.id);}));
$('delete').addEventListener('click',()=>mutation(async()=>{const id=state.selected.id;if(!window.confirm('Delete this run and its evidence? If selected as a baseline, that reference will be cleared.'))return;await api('runs/'+encodeURIComponent(id)+'?acknowledge=true','DELETE');state.selected=null;$('detail').hidden=true;state.extra=[];clearExport();}));
function showComparison(value){$('comparison').replaceChildren(text('p',(value.illustrative?'Illustrative sample comparison. ':'')+(value.partial?'Contains incomplete or failed runs. ':'')+value.reasons.join(' ')),table(['Measurement','Baseline','Candidate','Delta','Percent','Details'],value.metrics.map(m=>[m.name,m.baseline,m.candidate,m.delta,m.percent===undefined?'Unavailable':Number(m.percent.toPrecision(6))+'%',m.reason||m.unit])));for(const series of value.series){$('comparison').append(text('p',series.name+': '+(series.reason||'Matching grid; pointwise delta available.')));if(series.overlay){$('comparison').append(chart(series.baseline),chart(series.candidate));if(series.delta)$('comparison').append(chart({...series.candidate,name:series.name+' delta',y:series.delta}));}}}
$('compare').addEventListener('click',()=>mutation(async()=>showComparison(await api('comparisons','POST',{baseline:{kind:'run',id:state.baseline.run_id},candidate:{kind:'run',id:state.selected.id}}))));
$('samples').addEventListener('click',()=>{if(!live())return;api('comparisons','POST',{baseline:{kind:'sample',id:'benchmark-small'},candidate:{kind:'sample',id:'benchmark-large'}}).then(showComparison).catch(e=>notice(e.message));});
$('export-preview').addEventListener('click',()=>mutation(async()=>{const runs=state.baseline?.run_id&&state.baseline.run_id!==state.selected.id?[state.baseline.run_id,state.selected.id]:[state.selected.id];state.export=await api('exports/prepare','POST',{runs,options:{inputs:$('include-inputs').checked,notes:$('include-notes').checked,logs:$('include-logs').checked,source:$('include-source').checked}});$('export-text').textContent=JSON.stringify(state.export.document,null,2);$('export-review').hidden=false;}));
for(const format of ['html','json'])$('download-'+format).addEventListener('click',()=>mutation(async()=>{const file=await api('exports','POST',{preparation:state.export.id,digest:state.export.digest,format});const bytes=Uint8Array.from(atob(file.data_base64),c=>c.charCodeAt(0));const hash=Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256',bytes)),b=>b.toString(16).padStart(2,'0')).join('');if(hash!==file.sha256)throw new Error('Export integrity check failed.');const url=URL.createObjectURL(new Blob([bytes],{type:file.media_type}));const link=document.createElement('a');link.href=url;link.download='patchbay-report.'+format;link.click();setTimeout(()=>URL.revokeObjectURL(url),1000);clearExport();}));
for(const name of ['inputs','notes','logs','source'])$('include-'+name).addEventListener('change',()=>{clearExport();buttons();});
$('more').addEventListener('click',async()=>{try{const page=await api('runs?cursor='+encodeURIComponent(state.cursor)+'&project='+encodeURIComponent(state.data.context.project||'')+'&experiment='+encodeURIComponent(state.experiment));state.extra.push(...page.runs);state.cursor=page.next_cursor||'';renderHistory();}catch(error){notice(error.message);}});
$('refresh').addEventListener('click',()=>refresh());
$('quit').addEventListener('click',async()=>{try{await fetch('/session/quit',{method:'POST',headers:{Authorization:'Bearer '+token,'Content-Type':'application/json'},credentials:'omit'});}finally{token='';state.closed=true;clearPreview();clearExport();buttons();notice(state.owned?'The demo is closing. Saved results remain in its private workspace.':'This workbench session is closed. Daemon jobs continue running.');}});
buttons();setInterval(buttons,250);
if(!/^[A-Za-z0-9_-]{43}$/.test(token)){token='';notice('Launch deckctl workbench to open an authorized tab. A refresh deliberately removes browser access.');}
async function poll(){await refresh();if(!state.closed)setTimeout(poll,document.hidden||state.resync?2000:500);}
poll();
