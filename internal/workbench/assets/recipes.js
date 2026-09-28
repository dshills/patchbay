'use strict';
const recipeState={view:null,package:null,preview:null,export:null,list:null,xhr:null};
function clearRecipeReview(){recipeState.preview=null;recipeState.export=null;$('recipe-review').hidden=true;$('recipe-export-review').hidden=true;$('recipe-confirm').checked=false;$('recipe-export-confirm').checked=false;}
function recipeButtons(ready){
 const selected=!!recipeState.view;
 document.querySelectorAll('#recipe-editor input,#recipe-editor select,#recipe-file,#recipe-select,#recipe-stage-update').forEach(e=>{e.disabled=!ready;});
 $('recipe-upload').disabled=!ready||!$('recipe-file').files.length;
 $('recipe-load').disabled=!ready||!$('recipe-select').value;
 for(const id of ['recipe-prepare','recipe-export-prepare'])$(id).disabled=!ready||!selected;
 $('recipe-commit').disabled=!ready||!recipeState.preview||Date.now()>=Date.parse(recipeState.preview.expires_at)||!$('recipe-confirm').checked;
 $('recipe-export-save').disabled=!ready||!recipeState.export||Date.now()>=Date.parse(recipeState.export.expires_at)||!$('recipe-export-confirm').checked;
 $('recipe-compare').disabled=!ready||!$('recipe-sample-left').value||!$('recipe-sample-right').value;
 $('recipe-compare-run').disabled=!ready||!$('recipe-sample-left').value||!state.selected;
 if(recipeState.preview)$('recipe-expiry').textContent=Date.now()>=Date.parse(recipeState.preview.expires_at)?'Review expired. Prepare this change again.':'Review expires at '+new Date(recipeState.preview.expires_at).toLocaleTimeString();
}
function recipeRefresh(list){
 if(recipeState.list&&list.revision!==recipeState.list.revision)clearRecipeReview();recipeState.list=list;
 const current=$('recipe-select').value||recipeState.view?.installation.id||'';
 options($('recipe-select'),[{id:'',title:'Choose an installation'},...list.installations.map(v=>({id:v.installation.id,title:v.installation.alias+' · '+v.installation.id.slice(0,9)+' · '+v.status}))],current);
 $('recipe-storage').textContent=`${list.installations.length}/50 installations · ${(list.bytes/1048576).toFixed(1)} MiB stored · ${list.unused?.length||0} unused files. `+(list.diagnostics||[]).join(' ');
}
let recipeFieldID=0;
function recipeField(parent,labelText,element){const label=text('label',labelText);element.id='recipe-field-'+(++recipeFieldID);label.htmlFor=element.id;parent.append(label,element);return element;}
function recipeInput(parent,labelText,value=''){const input=document.createElement('input');input.value=value;return recipeField(parent,labelText,input);}
function recipeChecks(parent,group,values,selected){for(const name of values){const label=text('label','','check');const input=document.createElement('input');input.type='checkbox';input.dataset.group=group;input.value=name;input.checked=selected;label.append(input,document.createTextNode(' '+name));parent.append(label);}}
function recipeSelection(group){return [...document.querySelectorAll('#recipe-export-selection input')].filter(i=>i.dataset.group===group&&i.checked).map(i=>i.value);}
function renderRecipe(){
 const view=recipeState.view;if(!view)return;const pkg=view.package;recipeState.package=pkg;const m=pkg.manifest,entry=view.installation;
 $('recipe-editor').hidden=false;$('recipe-title').textContent=m.name+' · '+m.version;$('recipe-description').textContent=m.description;
 $('recipe-identity').textContent=`Declared ${m.id} by ${m.author} · ${m.license}. Local ID ${entry.id}. Content ${pkg.package_digest}. Attribution is unverified.`;
 $('recipe-diagnostics').textContent=[view.status,...view.diagnostics].join(' · ');$('recipe-alias').value=entry.alias;
 $('recipe-mappings').replaceChildren();
 for(const [name,role] of Object.entries(m.requirements||{})){
  const mapping=entry.mappings[name]||{};const box=document.createElement('div');box.className='recipe-role';box.dataset.role=name;box.dataset.kind=role.kind;
  box.append(text('p',name+' · '+role.kind+(role.optional?' (optional)':' (required)')+' · '+role.description));
  let input;if(role.kind==='project'){input=document.createElement('select');options(input,[{id:'',title:'Choose a project'},...state.data.projects.projects.map(p=>({id:p.id,title:p.name}))],mapping.project||'');}
  else{input=document.createElement('input');input.value=mapping[role.kind]||'';input.placeholder=role.kind==='tool'?'/absolute/path/to/executable':'Existing local action ID';}
  recipeField(box,'Local '+role.kind+' for '+name,input);input.dataset.mapping='true';
  if(role.provider)box.append(text('p',JSON.stringify(role),'muted'));$('recipe-mappings').append(box);
 }
 $('recipe-controls').replaceChildren();
 for(const [name,target] of Object.entries(m.controls||{})){
  const box=document.createElement('div');box.className='recipe-role';box.dataset.control=name;box.append(text('h4',name+' · '+JSON.stringify(target)));const a=entry.assignments[name]||{};
  recipeInput(box,'Device for '+name,a.device||'').dataset.assignment='device';recipeInput(box,'Control for '+name,a.control||'').dataset.assignment='control';
  const gesture=document.createElement('select');options(gesture,[{id:'',title:'Unassigned'},...['press','long_press','touch','long_touch','rotate'].map(id=>({id,title:id}))],a.gesture||'');recipeField(box,'Gesture for '+name,gesture).dataset.assignment='gesture';$('recipe-controls').append(box);
 }
 $('recipe-documents').replaceChildren();for(const doc of view.documents||[]){const d=document.createElement('details');d.append(text('summary',doc.path+(doc.truncated?' (excerpt)':'')),text('pre',doc.text));$('recipe-documents').append(d);}
 $('recipe-manifest').textContent=JSON.stringify(m,null,2);
 const samples=(pkg.samples||[]).map(s=>({id:s.id,title:s.id+' · illustrative'}));options($('recipe-sample-left'),samples,samples[0]?.id||'');options($('recipe-sample-right'),samples,samples[1]?.id||samples[0]?.id||'');
 const selection=$('recipe-export-selection');selection.replaceChildren();
 for(const group of ['actions','workflows','experiments','parameters','controls']){selection.append(text('h4',group));recipeChecks(selection,group,Object.keys(m[group]||{}),true);}
 selection.append(text('h4','Use current values as portable defaults (optional)'));recipeChecks(selection,'defaults',Object.keys(m.parameters||{}),false);
 selection.append(text('h4','Extra documentation (README and license are always included)'));recipeChecks(selection,'documentation',(m.inventory||[]).filter(f=>f.path.startsWith('docs/')).map(f=>f.path),false);
 selection.append(text('h4','Illustrative samples (optional)'));recipeChecks(selection,'samples',samples.map(s=>s.id),false);
 clearRecipeReview();buttons();
}
async function loadRecipe(id,operation='activate'){const entry=recipeState.view?.installation;let content='';if(operation==='update')content=entry?.candidate;if(operation==='rollback')content=entry?.previous;if(['update','rollback'].includes(operation)&&!content)throw new Error('No stored version is available for '+operation);recipeState.view=await api('recipes/'+encodeURIComponent(id)+(content?'?content='+encodeURIComponent(content):''));$('recipe-operation').value=operation;$('recipe-select').value=id;renderRecipe();}
function recipeMappings(){const mappings={},assignments={};for(const box of document.querySelectorAll('#recipe-mappings [data-role]')){const value=box.querySelector('[data-mapping]').value.trim();if(value)mappings[box.dataset.role]={[box.dataset.kind]:value};}for(const box of document.querySelectorAll('#recipe-controls [data-control]')){const a={};for(const input of box.querySelectorAll('[data-assignment]'))a[input.dataset.assignment]=input.value.trim();if(a.gesture){if(!a.control)throw new Error('Choose a control for '+box.dataset.control);assignments[box.dataset.control]=a;}}return {mappings,assignments};}
$('recipe-file').addEventListener('change',buttons);
$('recipe-upload').addEventListener('click',()=>mutation(async()=>{
 const file=$('recipe-file').files[0];if(!file||file.size>20*1048576)throw new Error('Choose a ZIP no larger than 20 MiB.');
 let query='';if($('recipe-stage-update').checked){if(!recipeState.view)throw new Error('Open the exact installation to update first.');query='?installation='+encodeURIComponent(recipeState.view.installation.id);}
 $('recipe-abort').hidden=false;$('recipe-progress').hidden=false;$('recipe-progress').value=0;$('recipe-upload-status').textContent='Uploading and verifying locally…';
 try{const result=await new Promise((resolve,reject)=>{const xhr=new XMLHttpRequest();recipeState.xhr=xhr;xhr.open('POST','/api/recipes/imports'+query);xhr.timeout=30000;xhr.setRequestHeader('Authorization','Bearer '+token);xhr.setRequestHeader('Content-Type','application/zip');xhr.upload.onprogress=e=>{if(e.lengthComputable)$('recipe-progress').value=100*e.loaded/e.total;};xhr.onload=()=>{try{const value=JSON.parse(xhr.responseText);if(xhr.status<200||xhr.status>=300)throw new Error(value.error?.message||'Import failed.');resolve(value);}catch(e){reject(e);}};xhr.onerror=xhr.ontimeout=()=>reject(new Error('Upload response lost. Inspect installed recipes; identical content can be imported again.'));xhr.onabort=()=>reject(new Error('Upload cancelled. Check installed recipes; cancellation does not activate a package.'));xhr.send(file);});$('recipe-upload-status').textContent='Imported. Review mappings before activation.';await loadRecipe(result.installation.id);}
 finally{recipeState.xhr=null;$('recipe-abort').hidden=true;$('recipe-progress').hidden=true;}
}));
$('recipe-abort').addEventListener('click',()=>recipeState.xhr?.abort());
$('recipe-load').addEventListener('click',()=>mutation(()=>loadRecipe($('recipe-select').value)));
$('recipe-operation').addEventListener('change',()=>mutation(()=>loadRecipe(recipeState.view.installation.id,$('recipe-operation').value)));
for(const id of ['recipe-alias','recipe-mappings','recipe-controls','recipe-reset','recipe-export-selection','recipe-export-run'])$(id).addEventListener('change',()=>{clearRecipeReview();buttons();});
for(const id of ['recipe-confirm','recipe-export-confirm'])$(id).addEventListener('change',buttons);
function showRecipeReview(preview){recipeState.preview=preview;$('recipe-review-text').textContent=JSON.stringify(preview,null,2);$('recipe-review').hidden=false;$('recipe-confirm').checked=false;$('recipe-review').scrollIntoView({block:'nearest'});}
$('recipe-prepare').addEventListener('click',()=>mutation(async()=>{const operation=$('recipe-operation').value;const request={operation,alias:$('recipe-alias').value.trim(),reset_parameters:$('recipe-reset').checked,...(operation==='rename'?{}:recipeMappings())};showRecipeReview(await api('recipes/'+recipeState.view.installation.id+'/prepare','POST',request));}));
$('recipe-cleanup').addEventListener('click',()=>mutation(async()=>{const preview=await api('recipes/store/prepare','POST',{operation:'cleanup'});$('recipe-editor').hidden=false;showRecipeReview(preview);}));
$('recipe-commit').addEventListener('click',()=>mutation(async()=>{const p=recipeState.preview;if(!p||!$('recipe-confirm').checked)return;const request={preparation:p.id,digest:p.digest,request_id:Date.now()+'-'+crypto.randomUUID().replaceAll('-',''),confirmed:true};try{const result=await api('recipes/'+p.installation.id+'/commit','POST',request);clearRecipeReview();$('recipe-result').textContent=result.operation+' committed at store revision '+result.revision+'. Request ID: '+request.request_id;if(result.operation==='remove'){recipeState.view=null;$('recipe-editor').hidden=true;}else if(p.installation.id!=='store')await loadRecipe(p.installation.id);}catch(error){if(!error.code){error.message+=' The change may have committed. No retry was sent. Retry this exact request with deckctl: '+JSON.stringify(request);}throw error;}}));
function recipeSample(id){return {kind:'recipe_sample',id,installation:recipeState.view.installation.id,content:recipeState.package.package_digest};}
$('recipe-compare').addEventListener('click',()=>mutation(async()=>showComparison(await api('comparisons','POST',{baseline:recipeSample($('recipe-sample-left').value),candidate:recipeSample($('recipe-sample-right').value)}))));
$('recipe-compare-run').addEventListener('click',()=>mutation(async()=>showComparison(await api('comparisons','POST',{baseline:recipeSample($('recipe-sample-left').value),candidate:{kind:'run',id:state.selected.id}}))));
$('recipe-export-prepare').addEventListener('click',()=>mutation(async()=>{const request={content:recipeState.package.package_digest};for(const group of ['actions','workflows','experiments','parameters','controls','defaults','documentation','samples'])request[group]=recipeSelection(group);if($('recipe-export-run').checked){if(!state.selected)throw new Error('Select a successful measured run first.');request.runs=[state.selected.id];}const p=await api('recipes/'+recipeState.view.installation.id+'/export/prepare','POST',request);recipeState.export=p;$('recipe-export-warning').textContent=p.warnings.join(' ');$('recipe-export-files').replaceChildren();for(const [name,content] of Object.entries(p.files)){const d=document.createElement('details');d.append(text('summary',name),text('pre',content));$('recipe-export-files').append(d);}$('recipe-export-confirm').checked=false;$('recipe-export-review').hidden=false;}));
$('recipe-export-save').addEventListener('click',()=>mutation(async()=>{const p=recipeState.export;if(!p||!$('recipe-export-confirm').checked)return;const file=await api('recipes/'+p.installation+'/export','POST',{preparation:p.id,digest:p.digest,confirmed:true});const bytes=Uint8Array.from(atob(file.data_base64),c=>c.charCodeAt(0));const digest=Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256',bytes)),b=>b.toString(16).padStart(2,'0')).join('');if(digest!==file.sha256||digest!==p.digest)throw new Error('Recipe download failed its integrity check.');const url=URL.createObjectURL(new Blob([bytes],{type:'application/zip'}));const link=document.createElement('a');link.href=url;link.download='patchbay-recipe.zip';link.click();setTimeout(()=>URL.revokeObjectURL(url),1000);recipeState.export=null;$('recipe-export-review').hidden=true;}));
