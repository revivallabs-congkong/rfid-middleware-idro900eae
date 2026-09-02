'use strict';
const $ = id => document.getElementById(id);
const MAXLOG = 2000;
let META = {}, LAST_STATE = null, CATALOG = null, CFG = null, WIZ = null;
let paused = false, selectTarget = null, cfgEditing = false;
const logBuf = [];

/* ── utils ── */
function esc(s){return String(s??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));}
function fmtTime(s){if(!s)return'—';const d=new Date(s);return isNaN(d)?s:d.toLocaleTimeString('ko-KR',{hour12:false});}
async function api(path,body){
  const opt=body?{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)}:undefined;
  return (await fetch(path,opt)).json();
}
function toast(msg){
  const t=document.createElement('div');t.className='toast';t.textContent=msg;$('toast').appendChild(t);
  setTimeout(()=>t.remove(),4000);
}
function modal(title,body,buttons){
  $('mTitle').textContent=title;$('mBody').textContent=body;
  const bb=$('mBtns');bb.innerHTML='';
  buttons.forEach(([label,cls,fn])=>{const b=document.createElement('button');b.textContent=label;
    b.className='btn'+(cls?' '+cls:'');b.onclick=()=>{hideModal();if(fn)fn();};bb.appendChild(b);});
  $('modal').classList.add('show');
}
function hideModal(){$('modal').classList.remove('show');}

/* ── mode & tabs ── */
function setMode(m){
  $('monitor').hidden=m!=='monitor';$('setup').hidden=m!=='setup';
  $('modeMonitor').setAttribute('aria-pressed',m==='monitor');
  $('modeSetup').setAttribute('aria-pressed',m==='setup');
  try{localStorage.setItem('ck_mode',m);}catch{}
}
$('modeMonitor').onclick=()=>setMode('monitor');
$('modeSetup').onclick=()=>{setMode('setup');loadCatalog();loadConfig();loadWizard();};
document.querySelectorAll('.tabs button').forEach(b=>{
  b.onclick=()=>{
    const name=b.dataset.tab;
    document.querySelectorAll('.tabs button').forEach(x=>x.setAttribute('aria-selected',x===b));
    ['session','check','config','log'].forEach(t=>$('tab-'+t).hidden=t!==name);
    if(name==='log')drawLog();
  };
});

/* ── state / monitor ── */
function statusWord(st){
  if(!st.collecting)return'수집 중지';
  return st.signal==='green'?'수집 중':st.signal==='yellow'?'주의':st.signal==='red'?'점검 필요':'수집 중';
}
function renderState(st){
  LAST_STATE=st;
  const sig=st.signal||'idle';
  const lampCls=sig==='green'?'live':sig==='yellow'?'warn':sig==='red'?'alert':'';
  const lamp=$('lamp');lamp.className='lamp'+(lampCls?' '+lampCls:'');
  $('statusWord').textContent=statusWord(st);
  $('statusMode').textContent=META.mode==='hosting'
    ?(st.coreRunning?'수집 호스팅':'수집 중지'):(st.coreRunning?'서비스 연결됨':'관측 모드');
  // band
  $('band').className='band'+(lampCls?' '+lampCls:'');
  $('headline').textContent=st.headline||'';
  chip($('chipCollect'),st.collecting?'on':'off',st.collecting?'수집 중':'수집 중지');
  chip($('chipNet'),st.network==='online'?'on':st.network==='offline'?'off':'wait',
    st.network==='online'?'인터넷 연결됨':st.network==='offline'?'인터넷 끊김':'인터넷 확인 중');
  // contextual action
  const act=$('bandAct');act.innerHTML='';
  if(!st.coreRunning){
    const b=document.createElement('button');b.className='btn primary';b.textContent='수집 시작';
    b.onclick=()=>META.mode==='hosting'?coreCtl('start','수집을 시작'):svcCtl('start','시작');
    act.appendChild(b);
  }
  renderReaders(st);
  renderCtrl(st);
  renderCatalogBanners(st);
  refreshWizardIfIdle();
}
function chip(el,cls,text){el.className='chip '+cls;el.querySelector('span:last-child').textContent=text;}

function renderReaders(st){
  const box=$('readers');box.innerHTML='';
  (st.readers||[]).forEach(r=>{
    const sess=r.sessionName?esc(r.sessionName)+(r.sessionVerified?' <span class="verified">✓</span>':' <span style="color:var(--ink-faint)">(확인 중)</span>')
      :esc(r.boothName||'세션 미지정');
    const up=r.connState==='CONNECTED';
    const card=document.createElement('div');card.className='card';
    let note='';
    if((r.gateState||'').startsWith('SUSPENDED'))note='<div class="cardnote bad">'+esc(r.actionText||'전송 중단됨')+'</div>';
    else if(r.actionText)note='<div class="cardnote">'+esc(r.actionText)+'</div>';
    let btns='';
    if((r.gateState||'').startsWith('SUSPENDED'))
      btns='<button class="btn sm primary" data-resume="'+esc(r.id)+'">재개…</button>';
    if(r.updateAvailable)
      btns+=' <button class="btn sm primary" data-reapply="'+esc(r.id)+'" data-s="'+esc(r.sessionId||'')+'">새 토큰 적용</button>';
    card.innerHTML=
      '<div class="chead"><span class="rid">'+esc(r.id)+'</span>'+
        '<span class="conn '+(up?'up':'down')+'"><span class="d"></span>'+(up?'연결됨':'끊김')+'</span></div>'+
      '<div class="sess">'+sess+(r.unitName?' · '+esc(r.unitName):'')+'</div>'+
      '<div class="metrics" style="grid-template-columns:repeat(3,1fr)">'+
        metric('대기',(r.pending||0)+'건','')+
        metric('마지막 태그',fmtTime(r.lastTagAt))+
        metric('마지막 성공',fmtTime(r.lastSuccessAt))+
      '</div>'+note+(btns?'<div class="cardbtns">'+btns+'</div>':'');
    box.appendChild(card);
  });
  box.querySelectorAll('[data-resume]').forEach(b=>b.onclick=()=>resumeDialog(b.dataset.resume));
  box.querySelectorAll('[data-reapply]').forEach(b=>b.onclick=()=>applySession(b.dataset.reapply,b.dataset.s));
  if(!(st.readers||[]).length)box.innerHTML='<div class="card"><div class="sess">리더 정보가 아직 없습니다. [점검·설정 › 설정]에서 리더를 확인하세요.</div></div>';
}
function metric(k,v,cls,mono){
  return '<div class="metric"><div class="mk">'+esc(k)+'</div><div class="mv '+(cls||'')+'">'+esc(v||'0')+'</div></div>';
}

/* ── check-in table ── */
const MAXCHK=200;
const checkins=[];               // {ts, gate, tag, kind, label}
const chkCount={ok:0,dup:0,miss:0,fail:0};
function resultOf(status){
  if(status===200)return{kind:'ok',label:'성공'};
  if(status===409)return{kind:'dup',label:'중복'};
  if(status===404)return{kind:'miss',label:'미등록'};
  return{kind:'fail',label:'실패 '+status};
}
// SEND_RESULT 로그 1건을 체크인 기록으로 적재.
function pushCheckin(o){
  const r=resultOf(o.httpStatus);
  chkCount[r.kind]++;
  checkins.push({ts:o.ts||'',gate:o.readerId||'',tag:o.epc||'',kind:r.kind,label:r.label});
  if(checkins.length>MAXCHK)checkins.shift();
  renderCheckins(true);
}
function renderCheckins(fresh){
  // 요약 칩
  $('chkStats').innerHTML=
    '<span class="stat ok">성공 <b>'+chkCount.ok+'</b></span>'+
    '<span class="stat dup">중복 <b>'+chkCount.dup+'</b></span>'+
    '<span class="stat miss">미등록 <b>'+chkCount.miss+'</b></span>'+
    (chkCount.fail?'<span class="stat fail">실패 <b>'+chkCount.fail+'</b></span>':'');
  const body=$('chkBody');
  if(!checkins.length){body.innerHTML='<tr><td colspan="4" class="chkempty">체크인이 시작되면 여기에 표시됩니다.</td></tr>';return;}
  // 최신이 위로, 최근 100건
  const rows=checkins.slice(-100).reverse();
  body.innerHTML=rows.map((c,i)=>
    '<tr'+(fresh&&i===0?' class="fresh"':'')+'>'+
    '<td class="t-time">'+esc((c.ts||'').slice(11,19))+'</td>'+
    '<td class="t-gate">'+esc(c.gate||'—')+'</td>'+
    '<td><span class="rbadge '+c.kind+'">'+esc(c.label)+'</span></td>'+
    '<td class="t-tag">'+esc(c.tag||'—')+'</td></tr>').join('');
}

/* ── control strip (system) ── */
function renderCtrl(st){
  const c=$('ctrl');if(!c)return;c.innerHTML='';
  const mk=(label,cls,fn)=>{const b=document.createElement('button');b.className='btn'+(cls?' '+cls:'');
    b.textContent=label;b.onclick=fn;c.appendChild(b);};
  const running=st&&st.coreRunning;
  if(META.mode==='hosting'){
    if(!running)mk('수집 시작','primary',()=>coreCtl('start','수집을 시작'));
    else{mk('수집 재시작','',()=>coreCtl('restart','수집을 재시작'));mk('수집 중지','danger',()=>coreCtl('stop','수집을 중지'));}
  }else{
    mk('서비스 시작','primary',()=>svcCtl('start','시작'));
    mk('서비스 중지','danger',()=>svcCtl('stop','중지'));
    const s=document.createElement('span');s.className='tag-note';s.textContent='관리자 확인(UAC)이 뜰 수 있습니다';c.appendChild(s);
  }
}
function coreCtl(action,label){
  modal('확인',label+'할까요?'+(action!=='start'?'\n중지 동안 태그가 기록되지 않습니다.':''),[
    ['취소','',null],['진행','primary',async()=>{const j=await api('api/control/core',{action,confirm:true});
      toast(j.ok?label+' 요청됨':(j.error?.message||'실패'));}]]);
}
function svcCtl(action,label){
  modal('확인','서비스를 '+label+'할까요?'+(action==='stop'?'\n중지하면 체크인 수집이 멈춥니다.':''),[
    ['취소','',null],['진행','primary',async()=>{const j=await api('api/control/service',{action,confirm:true});
      toast(j.ok?label+' 요청됨 — 잠시 후 갱신됩니다':(j.error?.message||'실패'));}]]);
}

/* ── catalog / sessions ── */
async function loadCatalog(){const j=await api('api/catalog');if(j.ok){CATALOG=j.data;renderCatalog();}}
function renderCatalogBanners(st){
  const c=st&&st.catalog,el=$('catBanners');if(!el)return;el.innerHTML='';
  if(!c)return;
  const add=(cls,text,btn)=>{const d=document.createElement('div');d.className='banner '+cls;
    const s=document.createElement('span');s.textContent=text;d.appendChild(s);
    if(btn){const sp=document.createElement('span');sp.className='bspacer';d.appendChild(sp);d.appendChild(btn);}el.appendChild(d);};
  if(c.pendingImport){const b=document.createElement('button');b.className='btn sm primary';b.textContent='가져오기';
    b.onclick=importCatalog;add('info','다운로드 폴더에서 새 카탈로그 파일을 발견했습니다.',b);}
  if(c.error)add('err','카탈로그 파일 오류: '+c.error+' (마지막 정상 목록 표시 중)');
  if(c.stale)add('warn','카탈로그가 7일 이상 지났습니다 — 콘솔에서 다시 내보내세요.');
  if(c.updateAvailable)add('warn','카탈로그에 새 토큰이 있습니다 — 해당 리더의 [새 토큰 적용]을 누르세요.');
}
function renderCatalog(){
  const c=CATALOG,bar=$('catbar'),box=$('sessions');if(!bar)return;
  if(!c||!c.loaded){bar.innerHTML='';
    box.innerHTML='<div class="banner info">카탈로그 파일이 없습니다. 콩콩 콘솔에서 「미들웨어용 JSON 다운로드」로 받아 <b>'+esc(c&&c.path||'pulse-sessions.json')+'</b> 위치에 두거나 다운로드 폴더에 두면 자동 감지합니다.</div>';return;}
  bar.innerHTML='이벤트 <b>'+esc(c.eventName)+'</b> · 기준 '+esc((c.exportedAt||'').slice(0,16).replace('T',' '))+
    ' <button class="btn sm" id="catRefresh">새로고침</button>'+
    (selectTarget?' <b>'+esc(selectTarget)+'</b> 리더에 지정할 세션을 클릭하세요 <button class="btn sm" id="pickCancel">취소</button>':'');
  const rows=(c.sessions||[]).map(s=>'<tr class="'+(selectTarget?'pick':'')+'" data-s="'+esc(s.id)+'">'+
    '<td>'+esc(s.name)+'</td><td>'+esc(s.unitName)+'</td><td>'+esc(s.tokenLabel||'—')+'</td>'+
    '<td>'+esc(s.assignedReaderId||'—')+'</td></tr>').join('');
  box.innerHTML='<table class="tbl"><tr><th>세션</th><th>유닛</th><th>라벨</th><th>지정된 리더</th></tr>'+rows+'</table>';
  const rf=$('catRefresh');if(rf)rf.onclick=async()=>{await api('api/catalog/refresh',{confirm:true});loadCatalog();};
  const pc=$('pickCancel');if(pc)pc.onclick=()=>{selectTarget=null;renderCatalog();};
  if(selectTarget)box.querySelectorAll('tr.pick').forEach(tr=>tr.onclick=()=>applySession(selectTarget,tr.dataset.s));
  // 리더별 세션 선택 버튼
  if(!selectTarget&&LAST_STATE){
    const strip=document.createElement('div');strip.style.marginTop='12px';strip.className='wsetup';
    (LAST_STATE.readers||[]).forEach(r=>{const b=document.createElement('button');b.className='btn sm';
      b.textContent=r.id+' 세션 선택';b.onclick=()=>{selectTarget=r.id;renderCatalog();box.scrollIntoView({behavior:'smooth'});};strip.appendChild(b);});
    box.appendChild(strip);
  }
}
function applySession(readerId,sessionId){
  const s=(CATALOG.sessions||[]).find(x=>x.id===sessionId)||{};
  modal('세션 지정',"'"+(s.name||sessionId)+"' 세션을 '"+readerId+"' 리더에 지정할까요?\n"+
    (META.mode==='hosting'?'적용을 위해 수집이 잠시 재시작됩니다.':'저장 후 서비스 재시작이 필요합니다.'),[
    ['취소','',null],['지정','primary',async()=>{
      const j=await api('api/readers/'+encodeURIComponent(readerId)+'/session',{sessionId,confirm:true});
      selectTarget=null;
      if(j.ok){toast(j.data.message||'적용됨');loadCatalog();}else modal('실패',j.error?.message||'오류',[['닫기','primary',null]]);
    }]]);
}
function resumeDialog(readerId){
  const r=(LAST_STATE?.readers||[]).find(x=>x.id===readerId)||{};
  const sid=r.updateAvailable?r.sessionId:'';
  modal('재개 — 대기 기록 처리','중단 동안 쌓인 미전송 '+(r.pending||0)+'건을 어떻게 할까요?'+
    (r.updateAvailable&&sid?'\n카탈로그의 새 토큰이 함께 적용됩니다.':''),[
    ['취소','',null],
    ['폐기하고 재개','danger',()=>doResume(readerId,'discard',sid)],
    ['전송하고 재개','primary',()=>doResume(readerId,'send',sid)]]);
}
async function doResume(readerId,pending,sessionId){
  const j=await api('api/readers/'+encodeURIComponent(readerId)+'/resume',{pending,sessionId:sessionId||'',confirm:true});
  if(j.ok)toast(j.data.message||'재개됨');else modal('재개 실패',j.error?.message||'오류',[['닫기','primary',null]]);
}
async function importCatalog(){
  const j=await api('api/catalog/import',{confirm:true});
  if(j.ok){CATALOG=j.data;renderCatalog();toast('카탈로그를 가져왔습니다');}else modal('가져오기 실패',j.error?.message||'',[['닫기','primary',null]]);
}

/* ── wizard ── */
const STEP_DEFS=[['0','설정·환경'],['1','리더 연결'],['2','서버 확인'],['3a','실태그 체크인'],['3b','무해 전송 시험'],['4','오프라인 복구']];
async function loadWizard(){const j=await api('api/wizard');if(j.ok){WIZ=j.data;renderWizard();}}
function wizardReaderIds(){
  const c=(CFG?.config?.readers||[]).map(r=>r.id).filter(Boolean);
  return c.length?c:(LAST_STATE?.readers||[]).map(r=>r.id).filter(Boolean);
}
function refreshWizardIfIdle(){if(WIZ&&!WIZ.running&&(!WIZ.steps||!WIZ.steps.length))renderWizard();}
function renderWizard(){
  const el=$('wizard'),w=WIZ||{};
  if(!w.running&&(!w.steps||w.steps.length===0)){
    const rids=wizardReaderIds();
    if(!rids.length){el.innerHTML='<div class="banner warn">리더 정보를 불러오는 중입니다. 설정에 리더가 없으면 [설정] 탭에서 추가하세요.</div>';return;}
    const ropts=rids.map(id=>'<option value="'+esc(id)+'">'+esc(id)+'</option>').join('');
    const chks=STEP_DEFS.map(([id,t])=>'<label><input type="checkbox" class="wchk" value="'+id+'"'+(id!=='4'?' checked':'')+'> '+t+'</label>').join('');
    el.innerHTML='<div class="wsetup">리더 <select id="wReader">'+ropts+'</select><button class="btn primary" id="wStart">점검 시작</button></div>'+
      '<div class="wchecks">'+chks+'</div>';
    $('wStart').onclick=startWizard;return;
  }
  let html='';
  (w.steps||[]).forEach(s=>{
    const icon={pass:'✅',fail:'❌',warn:'⚠️',running:'⏳',skip:'⏭️',pending:'○'}[s.status]||'○';
    const metrics=s.metrics?Object.entries(s.metrics).map(([k,v])=>k+': '+v).join(' · '):'';
    html+='<div class="wstep '+esc(s.status)+'"><div class="wicon">'+icon+'</div><div><div class="wt">'+esc(s.title)+'</div>'+
      (s.detail?'<div class="wd">'+esc(s.detail)+'</div>':'')+(s.action?'<div class="wd">→ '+esc(s.action)+'</div>':'')+
      (metrics?'<div class="wm">'+esc(metrics)+'</div>':'')+'</div></div>';
  });
  el.innerHTML=html+'<div class="wsetup" style="margin-top:10px">'+
    (w.running?(w.awaitTag?'<b>시험용 태그를 리더에 대세요</b> <button class="btn primary" id="wConfirm">스캔 시작</button> <button class="btn" id="wAbort">중단</button>'
      :'<span class="tag-note">점검 진행 중…</span> <button class="btn" id="wAbort">중단</button>')
    :'<button class="btn primary" id="wReport">리포트 저장</button> <button class="btn" id="wRestart">다시 점검</button>')+'</div>';
  const cf=$('wConfirm');if(cf)cf.onclick=()=>modal('실태그 체크인','실제 서버에 체크인 기록이 생성됩니다.\n반드시 시험용 태그를 사용하세요. 진행할까요?',[
    ['취소','',()=>api('api/wizard/abort',{})],['진행','primary',()=>api('api/wizard/confirm',{confirm:true})]]);
  const ab=$('wAbort');if(ab)ab.onclick=()=>api('api/wizard/abort',{});
  const rp=$('wReport');if(rp)rp.onclick=async()=>{const j=await api('api/wizard/report',{});toast(j.ok?'저장됨: '+j.data.path:(j.error?.message||'실패'));};
  const rs=$('wRestart');if(rs)rs.onclick=()=>{WIZ={steps:[]};renderWizard();};
}
function startWizard(){
  const steps=[...document.querySelectorAll('.wchk:checked')].map(c=>c.value);
  const readerId=$('wReader').value;if(!steps.length)return;
  api('api/wizard/start',{readerId,steps,confirm:true}).then(j=>{if(!j.ok)modal('시작 실패',j.error?.message||'',[['닫기','primary',null]]);});
}

/* ── config ── */
async function loadConfig(){const j=await api('api/config');if(j.ok){CFG=j.data;renderConfig();refreshWizardIfIdle();}}
function renderConfig(){
  const el=$('cfgView');if(!CFG){el.innerHTML='';return;}
  const c=CFG.config;
  if(!cfgEditing){
    const rlist=(c.readers||[]).map(r=>'· <b>'+esc(r.id)+'</b> — '+esc(r.addr)+
      (CFG.tokenSet&&CFG.tokenSet[r.id]?' <span class="verified">토큰 지정됨</span>':' <span style="color:var(--warn)">토큰 미지정</span>')).join('<br>');
    el.innerHTML='<div class="card-plain">서버 '+esc(c.apiHost)+'<br>데이터 폴더 '+esc(c.dataDir)+
      '<br>디바운스 '+c.debounceSec+'초 · 출력 '+c.powerGain+' · 부저 '+(c.buzzer?'켬':'끔')+' · 로그 '+esc(c.logLevel)+
      '<br>세션 파일 '+esc(c.sessionsFile||'(기본: config 옆 pulse-sessions.json)')+'<br><br>리더<br>'+rlist+'</div>';
    $('cfgEdit').textContent='편집';return;
  }
  const f=(label,key,type)=>'<div class="fld"><label>'+label+'</label><input data-k="'+key+'" type="'+(type||'text')+'" value="'+esc(c[key]??'')+'"></div>';
  el.innerHTML='<div class="card-plain">'+f('서버 apiHost','apiHost')+f('데이터 폴더','dataDir')+
    f('디바운스(초)','debounceSec','number')+f('큐 보관(시간)','queueMaxAgeHours','number')+
    f('요청 타임아웃(초)','requestTimeoutSec','number')+f('출력 powerGain','powerGain','number')+
    '<div class="fld"><label>부저</label><select data-k="buzzer"><option value="0"'+(c.buzzer?'':' selected')+'>끔</option><option value="1"'+(c.buzzer?' selected':'')+'>켬</option></select></div>'+
    '<div class="fld"><label>로그 레벨</label><select data-k="logLevel">'+['debug','info','warn','error'].map(l=>'<option'+(c.logLevel===l?' selected':'')+'>'+l+'</option>').join('')+'</select></div>'+
    f('세션 파일 경로','sessionsFile')+
    '<div style="margin-top:10px"><b>리더</b> <button class="btn sm" id="rdrAdd">+ 리더 추가</button></div><div id="rdrList"></div>'+
    '<div style="margin-top:14px"><button class="btn primary" id="cfgSave">저장 및 적용</button> <button class="btn" id="cfgCancel">취소</button></div></div>';
  $('cfgEdit').textContent='편집 취소';
  renderReaderRows(c.readers||[]);
  el.querySelectorAll('[data-k]').forEach(inp=>inp.oninput=()=>{const k=inp.dataset.k;c[k]=(inp.type==='number'||k==='buzzer')?Number(inp.value):inp.value;});
  $('rdrAdd').onclick=()=>{c.readers.push({id:'gate-'+String.fromCharCode(97+c.readers.length),addr:'192.168.9.6:5578'});renderReaderRows(c.readers);};
  $('cfgCancel').onclick=()=>{cfgEditing=false;loadConfig();};
  $('cfgSave').onclick=saveConfig;
}
function renderReaderRows(readers){
  const box=$('rdrList');box.innerHTML='';
  readers.forEach((r,i)=>{const row=document.createElement('div');row.className='rdrRow';
    row.innerHTML='<input placeholder="id" value="'+esc(r.id)+'" data-i="'+i+'" data-f="id">'+
      '<input placeholder="주소 (IP:포트)" value="'+esc(r.addr)+'" data-i="'+i+'" data-f="addr">'+
      '<button class="btn sm" data-del="'+i+'">삭제</button>';box.appendChild(row);});
  box.querySelectorAll('input').forEach(inp=>inp.oninput=()=>{readers[inp.dataset.i][inp.dataset.f]=inp.value;});
  box.querySelectorAll('[data-del]').forEach(b=>b.onclick=()=>{readers.splice(Number(b.dataset.del),1);renderReaderRows(readers);});
}
function saveConfig(){
  modal('설정 저장','설정을 저장하고 적용할까요?'+(META.mode==='hosting'?'\n적용을 위해 수집이 잠시 재시작됩니다.':'\n관측 모드에서는 서비스 재시작이 필요합니다.'),[
    ['취소','',null],['저장','primary',async()=>{const j=await api('api/config',{config:CFG.config,confirm:true});
      if(j.ok){cfgEditing=false;toast(j.data.message||'저장됨');loadConfig();loadCatalog();}else modal('저장 실패',j.error?.message||'',[['닫기','primary',null]]);}]]);
}

/* ── log ── */
function appendLog(raw){
  let o;try{o=JSON.parse(raw);}catch{o={event:'RAW',message:raw,level:'info'};}
  logBuf.push(o);if(logBuf.length>MAXLOG)logBuf.shift();
  if(o.event==='SCAN_ENQUEUED')pulseLamp();
  if(o.event==='SEND_RESULT')pushCheckin(o);
  if(!$('tab-log').hidden&&!paused)drawLog();
}
let pulseT=null;
function pulseLamp(){const l=$('lamp');l.classList.remove('pulse');void l.offsetWidth;l.classList.add('pulse');}
function drawLog(){
  const lv=$('fLevel').value,rd=$('fReader').value.trim(),ev=$('fEvent').value.trim(),el=$('logs');
  const atBottom=el.scrollTop+el.clientHeight>=el.scrollHeight-20;
  el.innerHTML=logBuf.filter(o=>(!lv||o.level===lv)&&(!rd||(o.readerId||'').includes(rd))&&(!ev||(o.event||'').includes(ev))).map(o=>{
    const t=(o.ts||'').slice(11,19),cls=o.level==='error'?'le':o.level==='warn'?'lw':o.level==='debug'?'ld':'';
    const extra=Object.entries(o).filter(([k])=>!['ts','level','event','readerId','message'].includes(k)).map(([k,v])=>k+'='+v).join(' ');
    return '<div class="'+cls+'">'+esc(t)+'  ['+esc(o.level||'?')+'] '+esc(o.event||'')+(o.readerId?' ('+esc(o.readerId)+')':'')+
      (o.message?' — '+esc(o.message):'')+(extra?'  '+esc(extra):'')+'</div>';
  }).join('');
  if(atBottom)el.scrollTop=el.scrollHeight;
}
['fLevel','fReader','fEvent'].forEach(id=>$(id).addEventListener('input',drawLog));
$('pause').onclick=()=>{paused=!paused;$('pause').textContent=paused?'재개':'일시정지';if(!paused)drawLog();};
$('cfgEdit').onclick=()=>{cfgEditing=!cfgEditing;renderConfig();};

/* ── init ── */
try{setMode(localStorage.getItem('ck_mode')||'monitor');}catch{setMode('monitor');}
fetch('api/meta').then(r=>r.json()).then(({data})=>{
  META=data;$('meta').textContent=(data.version||'')+(data.cfgFingerprint?' · cfg '+data.cfgFingerprint:'');
  if(data.configError)toast('설정 파일을 읽을 수 없습니다 — [설정]에서 확인하세요');
  renderCtrl(LAST_STATE);loadCatalog();loadConfig();loadWizard();
});
function connectSSE(){
  const es=new EventSource('api/events');
  es.addEventListener('state',e=>renderState(JSON.parse(e.data)));
  es.addEventListener('log',e=>appendLog(e.data));
  es.addEventListener('catalog',()=>loadCatalog());
  es.addEventListener('wizard',e=>{WIZ=JSON.parse(e.data);renderWizard();});
  es.onerror=()=>{es.close();setTimeout(connectSSE,2000);};
}
renderCheckins(false);
fetch('api/state').then(r=>r.json()).then(({data})=>renderState(data)).catch(()=>{});
connectSSE();
