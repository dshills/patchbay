'use strict';
const agentState={catalog:null,preview:null,session:null,artifacts:[],cursor:'',extra:[],project:''};
function clearAgentConsent(){agentState.preview=null;$('agent-consent-check').checked=false;$('agent-consent').hidden=true;}
function agentButtons(ready){
 const usable=ready&&agentState.catalog?.available;
 $('agent-context').disabled=!usable;$('agent-start').disabled=!usable||!agentState.preview||Date.now()>=Date.parse(agentState.preview.expires_at)||!$('agent-consent-check').checked;
 $('agent-cancel').disabled=!ready||!agentState.session||!['generating','awaiting_review','executing'].includes(agentState.session.state);
 $('agent-forget').disabled=!ready||!agentState.session||['generating','awaiting_review','executing'].includes(agentState.session.state);
 $('agent-add-artifacts').disabled=!ready||!state.selected;
 for(const id of ['agent-question','agent-files','agent-retain'])$(id).disabled=!ready;
 if(agentState.preview)$('agent-consent-expiry').textContent=Date.now()>=Date.parse(agentState.preview.expires_at)?'Consent expired. Review upload again.':'Consent expires '+new Date(agentState.preview.expires_at).toLocaleTimeString();
}
async function showAgentSession(id){
 const s=await api('agents/sessions/'+encodeURIComponent(id));agentState.session=s;$('agent-detail').hidden=false;$('agent-session-title').textContent=s.project+' · '+s.id;
 $('agent-session-state').textContent=s.state+(s.error?' · '+s.error.code+': '+s.error.message:'');$('agent-usage').textContent=s.model+' · '+s.input_bytes+' input bytes · '+(s.usage?'Actual usage: '+JSON.stringify(s.usage):'Token usage unavailable')+' · Price unknown';
 $('agent-output').textContent=s.output?.summary||s.text||'Waiting for a completed response. No partial response can execute work.';
 $('agent-sources').replaceChildren(text('h4','Selected sources'));
 for(const item of s.items||[]){const row=text('p',(item.path||item.run+' / '+item.artifact)+' · SHA-256 '+item.sha256);if(item.kind==='artifact'){const b=text('button','Open source run');b.addEventListener('click',()=>mutation(()=>selectRun(item.run)));row.append(b);}$('agent-sources').append(row);}
 if(s.unsupported_refs?.length)$('agent-sources').append(text('p','Unsupported model references (no evidence link): '+s.unsupported_refs.join(', ')));
}
async function agentRefresh(){
 if(!$('agents-open').open)return;
 const project=state.data?.context.project||'';if(project!==agentState.project){agentState.project=project;agentState.extra=[];agentState.session=null;agentState.artifacts=[];$('agent-artifacts').replaceChildren();$('agent-detail').hidden=true;clearAgentConsent();}
 const [catalog,list]=await Promise.all([api('agents/catalog'),api('agents/sessions?project='+encodeURIComponent(project))]);agentState.catalog=catalog;if(!agentState.extra.length)agentState.cursor=list.next_cursor||'';
 $('agent-availability').textContent=catalog.message||(catalog.enabled?'Provider ready · '+catalog.model:'Proposal mode disabled');
 const sessions=[...new Map([...list.sessions,...agentState.extra].map(s=>[s.id,s])).values()];const stamp=JSON.stringify(sessions.map(s=>[s.id,s.state]));if($('agent-sessions').dataset.stamp!==stamp){$('agent-sessions').dataset.stamp=stamp;$('agent-sessions').replaceChildren(...sessions.map(s=>{const b=text('button',s.project+' · '+s.id.slice(0,8)+' · '+s.state);b.addEventListener('click',()=>mutation(()=>showAgentSession(s.id)));return b;}));}
 $('agent-more').hidden=!agentState.cursor;if(agentState.session)await showAgentSession(agentState.session.id);
}
$('agents-open').addEventListener('toggle',()=>{if($('agents-open').open)refresh();});
for(const id of ['agent-question','agent-files','agent-retain'])$(id).addEventListener('input',()=>{clearAgentConsent();buttons();});
$('agent-consent-check').addEventListener('change',buttons);
$('agent-add-artifacts').addEventListener('click',()=>{clearAgentConsent();const run=state.selected?.id;agentState.artifacts=[];$('agent-artifacts').replaceChildren();for(const a of state.selected?.artifacts||[]){const label=text('label',a.name+' · '+a.size+' bytes','check'),input=document.createElement('input');input.type='checkbox';input.addEventListener('change',()=>{clearAgentConsent();const ref={run,artifact:a.id};agentState.artifacts=agentState.artifacts.filter(x=>x.artifact!==a.id);if(input.checked)agentState.artifacts.push(ref);buttons();});label.prepend(input);$('agent-artifacts').append(label);}});
$('agent-context').addEventListener('click',()=>mutation(async()=>{const selection={prompt:$('agent-question').value,files:$('agent-files').value.split('\n').map(x=>x.trim()).filter(Boolean),artifacts:agentState.artifacts,retain_snapshots:$('agent-retain').checked,request_id:Date.now()+'-'+crypto.randomUUID().replaceAll('-','')};agentState.preview=await api('agents/context/prepare','POST',selection);$('agent-context-text').textContent=JSON.stringify(agentState.preview,null,2);$('agent-consent').hidden=false;$('agent-consent-check').checked=false;}));
$('agent-start').addEventListener('click',()=>mutation(async()=>{const p=agentState.preview;if(!p||!$('agent-consent-check').checked)return;const request={preparation:p.id,digest:p.digest,request_id:p.request_id,confirmed:true};try{const s=await api('agents/sessions','POST',request);clearAgentConsent();await showAgentSession(s.id);}catch(e){if(!e.code)e.message+=' Outcome unknown. No retry sent; inspect sessions or retry this exact JSON: '+JSON.stringify(request);throw e;}}));
$('agent-cancel').addEventListener('click',()=>mutation(async()=>{const id=agentState.session?.id;if(id){await api('agents/sessions/'+encodeURIComponent(id),'DELETE');await showAgentSession(id);}}));
$('agent-forget').addEventListener('click',()=>mutation(async()=>{const id=agentState.session?.id;if(!id||!window.confirm('Forget this session’s local context and audit? Linked runs remain.'))return;await api('agents/sessions/'+encodeURIComponent(id)+'/forget','POST',{confirmed:true});agentState.session=null;agentState.extra=[];$('agent-detail').hidden=true;}));
$('agent-more').addEventListener('click',()=>mutation(async()=>{const list=await api('agents/sessions?project='+encodeURIComponent(agentState.project)+'&cursor='+encodeURIComponent(agentState.cursor));agentState.extra.push(...list.sessions);agentState.cursor=list.next_cursor||'';}));
