'use strict';
const agentState={catalog:null,preview:null,session:null,artifacts:[],cursor:'',extra:[],project:'',selection:null,review:null,paired:false,reviewError:'',requested:0};
function clearAgentConsent(){agentState.paired=false;$('agent-approve-check').checked=false;agentState.preview=null;$('agent-consent-check').checked=false;$('agent-consent').hidden=true;}
function agentButtons(ready){
 const usable=ready&&agentState.catalog?.available;
 $('agent-context').disabled=!usable;$('agent-start').disabled=!usable||!agentState.preview||Date.now()>=Date.parse(agentState.preview.expires_at)||!$('agent-consent-check').checked;
 $('agent-cancel').disabled=!ready||!agentState.session||!['generating','awaiting_review','executing'].includes(agentState.session.state);
 $('agent-forget').disabled=!ready||!agentState.session||['generating','awaiting_review','executing'].includes(agentState.session.state);
 $('agent-add-artifacts').disabled=!ready||!state.selected;
 proposalButtons(ready);
 for(const id of ['agent-question' ,'agent-files','agent-retain'])$(id).disabled=!ready;
 if(agentState.preview)$('agent-consent-expiry').textContent=Date.now()>=Date.parse(agentState.preview.expires_at)?'Consent expired. Review upload again.':'Consent expires '+new Date(agentState.preview.expires_at).toLocaleTimeString();
}
async function showAgentSession(id){
 const s=await api('agents/sessions/'+encodeURIComponent(id));agentState.session=s;$('agent-detail').hidden=false;$('agent-session-title').textContent=s.project+' · '+s.id;
 $('agent-session-state').textContent=s.state+(s.error?' · '+s.error.code+': '+s.error.message:'');$('agent-usage').textContent=s.model+' · '+s.input_bytes+' input bytes · '+(s.usage?'Actual usage: '+JSON.stringify(s.usage):'Token usage unavailable')+' · Price unknown';
 $('agent-output').textContent=s.output?.summary||s.text||'Waiting for a completed response. No partial response can execute work.';
 $('agent-sources').replaceChildren(text('h4','Selected sources'));
 for(const item of s.items||[]){const row=text('p',(item.path||item.run+' / '+item.artifact)+' · SHA-256 '+item.sha256);if(item.kind==='artifact'){const b=text('button','Open source run');b.addEventListener('click',()=>mutation(()=>selectRun(item.run)));row.append(b);}$('agent-sources').append(row);}
 renderAgentProposals(s);
 if(s.unsupported_refs?.length)$('agent-sources').append(text('p','Unsupported model references (no evidence link): '+s.unsupported_refs.join(', ')));
}
async function agentRefresh(){
 if(state.data?.capabilities?.features?.agent_supervision===1){
 const selection=await api('agents/selection');
 if(agentState.selection?.revision!==selection.revision){agentState.paired=false;$('agent-approve-check').checked=false;}
 agentState.selection=selection;
 $('agent-selection').textContent=selection.project+' · selected '+(selection.proposal||selection.job||'none')+' · '+(selection.state||'')+(selection.review_active?' · Full review connected':'');
 if(selection.review_requested && selection.review_requested!==agentState.requested && selection.proposal){agentState.requested=selection.review_requested;$('agents-open').open=true;await showAgentSession(selection.session);await reviewAgentProposal(selection.proposal,false);}
 if(!$('agents-open').open){agentState.paired=false;return;}
 if(agentState.paired&&!document.hidden&&live()&&agentState.review&&Date.now()<Date.parse(agentState.review.expires_at)){
 try{agentState.selection=await api('agents/review','POST',{revision:selection.revision,preparation:agentState.review.id,digest:agentState.review.digest,renew:true});}catch(e){agentState.paired=false;agentState.reviewError=e.message;}
 }
 }
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

function agentRequestID(){return Date.now()+'-'+crypto.randomUUID().replaceAll('-','');}
function renderAgentProposals(s){
 const stamp=JSON.stringify(s.proposals||[]);if($('agent-proposals').dataset.stamp===stamp)return;$('agent-proposals').dataset.stamp=stamp;
 $('agent-proposals').replaceChildren(text('h4','Proposals · each needs its own approval'));
 for(const p of s.proposals||[]){const row=document.createElement('div');row.append(text('p',p.id.slice(0,8)+' · '+p.suggestion.kind+' '+p.suggestion.target+' · '+p.state+(p.message?' · '+p.message:'')));
 const b=text('button','Review '+p.id.slice(0,8));b.className='mutation';b.addEventListener('click',()=>mutation(()=>reviewAgentProposal(p.id,true)));row.append(b);
 if(p.run_id){const run=text('button','Open measured result '+p.run_id.slice(0,8));run.addEventListener('click',()=>mutation(async()=>{await selectRun(p.run_id);state.experiment=state.selected.experiment.id;await baseline();if(p.suggestion.baseline_run_id){const comparison=await api('comparisons','POST',{baseline:{kind:"run",id:p.suggestion.baseline_run_id},candidate:{kind:"run",id:p.run_id}});showComparison(comparison);}}));row.append(run);}
 $('agent-proposals').append(row);}
}
function proposalButtons(ready){
 const p=agentState.review, selected=p&&p.proposal===agentState.selection?.proposal;
 const pending=agentState.session?.proposals?.find(x=>x.id===p?.proposal);
 const valid=selected&&pending?.state==='pending'&&Date.now()<Date.parse(p.expires_at)&&!agentState.reviewError;
 $('agent-review-refresh').disabled=!ready||!selected;
 $('agent-approve').disabled=!ready||!valid||!$('agent-approve-check').checked;
 $('agent-reject').disabled=!ready||!selected||pending?.state!=='pending';
 $('agent-duplicate').disabled=!ready||!selected||!pending||Date.now()<Date.parse(pending.expires_at)||!['pending','expired'].includes(pending.state);
 $('agent-select-job').disabled=!ready||!agentState.session?.job_id||!['generating','executing'].includes(agentState.session.state);
 if(p)$('agent-review-state').textContent=agentState.reviewError||(!selected?'Selection changed. Choose this proposal again.':pending?.state!=='pending'?'Proposal '+pending?.state:Date.now()>=Date.parse(p.expires_at)?'Approval token expired. Your review remains here; refresh it without another model request.':'Review expires '+new Date(p.expires_at).toLocaleTimeString()+(agentState.paired?' · Deck paired while this full review stays connected.':' · Deck requires a refreshed review.'));
}
function effectiveReview(p){const value=structuredClone(p);for(const object of [value,value.capture]){if(object)for(const key of ['id','digest','expires_at'])delete object[key];}return JSON.stringify(value,null,2);}
async function reviewAgentProposal(id,select){
 if(select){agentState.selection=await api('agents/selection','PUT',{revision:agentState.selection?.revision||0,proposal:id});agentState.paired=false;}
 try{const p=await api('agents/proposals/'+encodeURIComponent(id)+'/prepare','POST',{});const changed=!agentState.review||effectiveReview(agentState.review)!==effectiveReview(p);
 agentState.review=p;agentState.reviewError='';$('agent-review').hidden=false;
 if(changed){$('agent-review-text').textContent=effectiveReview(p);$('agent-review-text').scrollTop=0;$('agent-approve-check').checked=false;}
 agentState.selection=await api('agents/review','POST',{revision:agentState.selection.revision,preparation:p.id,digest:p.digest});agentState.paired=true;
 if(select)$('agent-review-text').focus();
 }catch(e){if(!agentState.review||agentState.review.proposal!==id){agentState.review={proposal:id,expires_at:new Date(0).toISOString()};$('agent-review').hidden=false;$('agent-review-text').textContent=JSON.stringify(agentState.session?.proposals?.find(p=>p.id===id),null,2);}$('agent-approve-check').checked=false;agentState.paired=false;agentState.reviewError=e.message;throw e;}
}
$('agent-review-refresh').addEventListener('click',()=>mutation(()=>reviewAgentProposal(agentState.review.proposal,false)));
$('agent-approve-check').addEventListener('change',buttons);
$('agent-approve').addEventListener('click',()=>mutation(async()=>{const p=agentState.review;if(!p||!$('agent-approve-check').checked)return;const request={preparation:p.id,digest:p.digest,request_id:agentRequestID(),confirmed:true};try{const result=await api('agents/proposals/'+encodeURIComponent(p.proposal)+'/approve','POST',request);agentState.paired=false;$('agent-approve-check').checked=false;await selectRun(result.run_id);}catch(e){agentState.paired=false;if(!e.code)e.message+=' Outcome unknown; no retry sent. Inspect sessions or retry this exact JSON: '+JSON.stringify(request);throw e;}}));
$('agent-reject').addEventListener('click',()=>mutation(async()=>{await api('agents/proposals/'+encodeURIComponent(agentState.review.proposal)+'/reject','POST',{});agentState.paired=false;}));
$('agent-duplicate').addEventListener('click',()=>mutation(async()=>{const s=await api('agents/proposals/'+encodeURIComponent(agentState.review.proposal)+'/duplicate','POST',{request_id:agentRequestID()});await showAgentSession(s.id);await reviewAgentProposal(s.proposals[0].id,true);}));
$('agent-select-job').addEventListener('click',()=>mutation(async()=>{const s=agentState.session;agentState.selection=await api('agents/selection','PUT',{revision:agentState.selection.revision,session:s.id,job:s.job_id});agentState.paired=false;}));
document.addEventListener('visibilitychange',()=>{if(document.hidden){agentState.paired=false;$('agent-approve-check').checked=false;}});
