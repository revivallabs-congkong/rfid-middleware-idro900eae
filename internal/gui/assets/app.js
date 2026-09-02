'use strict';
const $ = id => document.getElementById(id);
const MAXLOG = 2000;
let paused = false;
let META = {};
let CATALOG = null;
let LAST_STATE = null;
let selectTarget = null; // 세션 선택 대상 리더 id
const logBuf = [];

function esc(s) {
  return String(s ?? '').replace(/[&<>"']/g, c =>
    ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
}
function fmtTime(s) {
  if (!s) return '—';
  const d = new Date(s);
  return isNaN(d) ? s : d.toLocaleTimeString('ko-KR', {hour12: false});
}
async function api(path, body) {
  const opt = body
    ? {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(body)}
    : undefined;
  const r = await fetch(path, opt);
  return r.json();
}

// ─── 모달 ───
function modal(title, bodyText, buttons) {
  $('mTitle').textContent = title;
  $('mBody').textContent = bodyText;
  const bb = $('mBtns');
  bb.innerHTML = '';
  buttons.forEach(([label, cls, fn]) => {
    const b = document.createElement('button');
    b.textContent = label;
    if (cls) b.className = cls;
    b.onclick = () => { hideModal(); if (fn) fn(); };
    bb.appendChild(b);
  });
  $('modal').style.display = 'flex';
}
function hideModal() { $('modal').style.display = 'none'; }

// ─── 상태 렌더 ───
function renderState(st) {
  LAST_STATE = st;
  $('dot').className = st.signal || 'gray';
  $('headline').textContent = st.headline || '';
  // 수집 인디케이터
  const ic = $('indCollect');
  ic.className = 'ind ' + (st.collecting ? 'on' : 'off');
  ic.querySelector('.itxt').textContent = st.collecting ? '수집 중' : '수집 중지';
  // 인터넷 인디케이터
  const inet = $('indNet');
  inet.className = 'ind ' + (st.network === 'online' ? 'on' : st.network === 'offline' ? 'off' : 'wait');
  inet.querySelector('.itxt').textContent =
    st.network === 'online' ? '인터넷 연결됨' : st.network === 'offline' ? '인터넷 끊김' : '인터넷 확인 중';
  $('mode').textContent = META.mode === 'hosting'
    ? (st.coreRunning ? '호스팅 모드 — 수집 중' : '호스팅 모드 — 수집 중지')
    : (st.coreRunning ? '관측 모드 — 서비스 동작 중' : '관측 모드');
  const rs = $('readers');
  rs.innerHTML = '';
  (st.readers || []).forEach(r => {
    const cls = r.gateState === 'ACTIVE' ? 'state-ok'
      : (r.gateState || '').startsWith('SUSPENDED') ? 'state-bad' : 'state-warn';
    const conn = r.connState === 'CONNECTED'
      ? '<span class="state-ok">연결됨</span>' : '<span class="state-bad">끊김</span>';
    const sessLine = r.sessionName
      ? esc(r.sessionName) + (r.sessionVerified ? ' ✓' : ' (확인 중)')
      : esc(r.boothName || '세션 미지정');
    const card = document.createElement('div');
    card.className = 'card';
    let btns = `<button data-act="pick" data-r="${esc(r.id)}">세션 선택</button>`;
    if ((r.gateState || '').startsWith('SUSPENDED')) {
      btns += ` <button class="primary" data-act="resume" data-r="${esc(r.id)}">재개…</button>`;
    }
    if (r.updateAvailable) {
      btns += ` <button class="primary" data-act="reapply" data-r="${esc(r.id)}" data-s="${esc(r.sessionId)}">새 토큰 적용</button>`;
    }
    card.innerHTML =
      '<div class="rid"><span>' + esc(r.id) + '</span></div>' +
      '<div class="sess">' + sessLine + (r.unitName ? ' · ' + esc(r.unitName) : '') + '</div>' +
      '<dl class="kv">' +
      '<dt>리더 연결</dt><dd>' + conn + '</dd>' +
      '<dt>전송 상태</dt><dd class="' + cls + '">' + esc(r.gateText || r.gateState) + '</dd>' +
      '<dt>마지막 태그</dt><dd>' + fmtTime(r.lastTagAt) + '</dd>' +
      '<dt>마지막 성공</dt><dd>' + fmtTime(r.lastSuccessAt) + '</dd>' +
      '<dt>대기 큐</dt><dd>' + (r.pending || 0) + '건</dd>' +
      '</dl>' +
      (r.actionText ? '<div class="action">👉 ' + esc(r.actionText) + '</div>' : '') +
      '<div class="cardbtns">' + btns + '</div>';
    rs.appendChild(card);
  });
  rs.querySelectorAll('button').forEach(b => {
    const rid = b.dataset.r;
    if (b.dataset.act === 'pick') b.onclick = () => startPick(rid);
    if (b.dataset.act === 'resume') b.onclick = () => resumeDialog(rid, null);
    if (b.dataset.act === 'reapply') b.onclick = () => reapply(rid, b.dataset.s);
  });
  $('queue').textContent = st.coreRunning
    ? '전체 미전송 ' + (st.queueDepth || 0) + '건 · 상태 갱신 ' + fmtTime(st.updatedAt) +
      (st.ntp && st.ntp.checked ? ' · 시계 오차 ' + st.ntp.skewSec + '초' : '') +
      (st.logDropped ? ' · 로그 드랍 ' + st.logDropped : '')
    : '';
  renderCtrl(st);
  renderCatalogBanners(st);
  refreshWizardIfIdle();
}

function renderCtrl(st) {
  const c = $('ctrl');
  c.innerHTML = '';
  const mk = (label, cls, fn) => {
    const b = document.createElement('button');
    b.textContent = label;
    if (cls) b.className = cls;
    b.onclick = fn;
    c.appendChild(b);
  };
  if (META.mode === 'hosting') {
    if (st && st.coreRunning === false) mk('수집 시작', 'primary', () => coreCtl('start', '수집을 시작'));
    else {
      mk('수집 재시작', '', () => coreCtl('restart', '수집을 재시작'));
      mk('수집 중지', '', () => coreCtl('stop', '수집을 중지'));
    }
  } else {
    mk('서비스 시작', 'primary', () => svcCtl('start', '시작'));
    mk('서비스 중지', '', () => svcCtl('stop', '중지'));
    const s = document.createElement('span');
    s.className = 'badge';
    s.textContent = '관리자 확인(UAC)이 뜰 수 있습니다';
    c.appendChild(s);
  }
  const m = document.createElement('span');
  m.className = 'badge';
  m.id = 'ctrlMsg';
  m.style.display = 'none';
  c.appendChild(m);
}

function ctrlMsg(t) {
  const m = $('ctrlMsg');
  if (!m) return;
  m.style.display = 'inline';
  m.textContent = t;
}

function coreCtl(action, label) {
  modal('확인', label + '할까요?' + (action !== 'start' ? '\n중지 동안 태그가 기록되지 않습니다.' : ''), [
    ['취소', '', null],
    ['진행', 'primary', async () => {
      const j = await api('api/control/core', {action, confirm: true});
      ctrlMsg(j.ok ? '완료' : (j.error?.message || '실패'));
    }],
  ]);
}
function svcCtl(action, label) {
  modal('확인', '서비스를 ' + label + '할까요?' + (action === 'stop' ? '\n중지하면 체크인 수집이 멈춥니다.' : ''), [
    ['취소', '', null],
    ['진행', 'primary', async () => {
      const j = await api('api/control/service', {action, confirm: true});
      ctrlMsg(j.ok ? '요청 완료 — 잠시 후 상태가 갱신됩니다' : (j.error?.message || '실패'));
    }],
  ]);
}

// ─── 카탈로그 ───
async function loadCatalog() {
  const j = await api('api/catalog');
  if (j.ok) { CATALOG = j.data; renderCatalog(); }
}

function renderCatalogBanners(st) {
  const c = st && st.catalog;
  const el = $('catBanners');
  el.innerHTML = '';
  if (!c) return;
  const add = (cls, text, btn) => {
    const d = document.createElement('div');
    d.className = 'banner ' + cls;
    d.textContent = text + ' ';
    if (btn) d.appendChild(btn);
    el.appendChild(d);
  };
  if (c.pendingImport) {
    const b = document.createElement('button');
    b.className = 'primary';
    b.textContent = '가져오기';
    b.onclick = importCatalog;
    add('info', '다운로드 폴더에서 새 카탈로그 파일을 발견했습니다.', b);
  }
  if (c.error) add('err', '카탈로그 파일 오류: ' + c.error + ' (마지막 정상 목록을 표시 중)');
  if (c.stale) add('warn', '카탈로그가 7일 이상 지났습니다 — 콘솔에서 다시 내보내는 것을 권장합니다.');
  if (c.updateAvailable) add('warn', '카탈로그에 새 토큰이 있습니다 — 해당 리더 카드의 [새 토큰 적용]을 누르세요.');
}

function renderCatalog() {
  const c = CATALOG;
  const bar = $('catbar');
  const box = $('sessions');
  if (!c || !c.loaded) {
    bar.innerHTML = '';
    box.innerHTML = '<div class="banner info">카탈로그 파일이 없습니다 — 콩콩 콘솔에서 「미들웨어용 JSON 다운로드」로 받아 ' +
      '<b>' + esc(c && c.path || 'pulse-sessions.json') + '</b> 위치에 두거나, 다운로드 폴더에 두면 자동으로 감지합니다.</div>';
    return;
  }
  bar.innerHTML = '이벤트: <b>' + esc(c.eventName) + '</b> · 기준 ' + esc((c.exportedAt || '').slice(0, 16).replace('T', ' ')) +
    ' &nbsp;<button id="catRefresh">새로고침</button>' +
    (selectTarget ? ' <span class="badge">👇 <b>' + esc(selectTarget) + '</b> 리더에 지정할 세션을 클릭하세요 <button id="pickCancel">취소</button></span>' : '');
  const rows = (c.sessions || []).map(s =>
    '<tr class="' + (selectTarget ? 'selectable' : '') + '" data-s="' + esc(s.id) + '">' +
    '<td>' + esc(s.name) + '</td><td>' + esc(s.unitName) + '</td>' +
    '<td>' + esc(s.tokenLabel || '—') + '</td>' +
    '<td>' + esc(s.assignedReaderId || '—') + '</td>' +
    '<td>' + esc((s.issuedAt || '').slice(0, 10)) + '</td></tr>').join('');
  box.innerHTML = '<table><tr><th>세션</th><th>유닛</th><th>라벨</th><th>지정된 리더</th><th>발급일</th></tr>' + rows + '</table>';
  const rf = $('catRefresh');
  if (rf) rf.onclick = async () => { await api('api/catalog/refresh', {confirm: true}); loadCatalog(); };
  const pc = $('pickCancel');
  if (pc) pc.onclick = () => { selectTarget = null; renderCatalog(); };
  if (selectTarget) {
    box.querySelectorAll('tr.selectable').forEach(tr => {
      tr.onclick = () => applySession(selectTarget, tr.dataset.s);
    });
  }
}

function startPick(readerId) {
  selectTarget = readerId;
  renderCatalog();
  $('sessions').scrollIntoView({behavior: 'smooth'});
}

function applySession(readerId, sessionId) {
  const s = (CATALOG.sessions || []).find(x => x.id === sessionId) || {};
  modal('세션 지정', "'" + (s.name || sessionId) + "' 세션을 '" + readerId + "' 리더에 지정할까요?\n" +
    (META.mode === 'hosting' ? '적용을 위해 수집이 잠시 재시작됩니다.' : '저장 후 서비스 재시작이 필요합니다.'), [
    ['취소', '', null],
    ['지정', 'primary', async () => {
      const j = await api('api/readers/' + encodeURIComponent(readerId) + '/session',
        {sessionId, confirm: true});
      selectTarget = null;
      if (j.ok) { ctrlMsg(j.data.message || '적용됨'); loadCatalog(); }
      else modal('실패', j.error?.message || '알 수 없는 오류', [['닫기', 'primary', null]]);
    }],
  ]);
}

function reapply(readerId, sessionId) {
  applySession(readerId, sessionId);
}

function resumeDialog(readerId, sessionId) {
  const r = (LAST_STATE?.readers || []).find(x => x.id === readerId) || {};
  const extra = r.updateAvailable && r.sessionId
    ? '\n카탈로그의 새 토큰이 함께 적용됩니다.' : '';
  const sid = r.updateAvailable ? r.sessionId : (sessionId || '');
  modal('재개 — 대기 기록 처리', '중단 동안 쌓인 미전송 ' + (r.pending || 0) + '건을 어떻게 할까요?' + extra, [
    ['취소', '', null],
    ['폐기하고 재개', '', () => doResume(readerId, 'discard', sid)],
    ['전송하고 재개', 'primary', () => doResume(readerId, 'send', sid)],
  ]);
}

async function doResume(readerId, pending, sessionId) {
  const j = await api('api/readers/' + encodeURIComponent(readerId) + '/resume',
    {pending, sessionId: sessionId || '', confirm: true});
  if (j.ok) ctrlMsg(j.data.message || '재개됨');
  else modal('재개 실패', j.error?.message || '알 수 없는 오류', [['닫기', 'primary', null]]);
}

async function importCatalog() {
  const j = await api('api/catalog/import', {confirm: true});
  if (j.ok) { CATALOG = j.data; renderCatalog(); ctrlMsg('카탈로그를 가져왔습니다'); }
  else modal('가져오기 실패', j.error?.message || '', [['닫기', 'primary', null]]);
}

// ─── 현장 점검 마법사 ───
let WIZ = null;
const STEP_DEFS = [
  ['0', '설정·환경'], ['1', '리더 연결'], ['2', '서버 확인'],
  ['3a', '실태그 체크인'], ['3b', '무해 전송 시험'], ['4', '오프라인 복구'],
];
async function loadWizard() {
  const j = await api('api/wizard');
  if (j.ok) { WIZ = j.data; renderWizard(); }
}
// 리더 목록 소스 — config(항상 존재) 우선, 없으면 실행 상태(status.json)에서.
function wizardReaderIds() {
  const fromCfg = (CFG?.config?.readers || []).map(r => r.id).filter(Boolean);
  if (fromCfg.length) return fromCfg;
  return (LAST_STATE?.readers || []).map(r => r.id).filter(Boolean);
}
// 마법사 시작 화면이 떠 있으면(대기 상태) 리더 목록이 채워지도록 다시 그린다.
function refreshWizardIfIdle() {
  if (WIZ && !WIZ.running && (!WIZ.steps || WIZ.steps.length === 0)) renderWizard();
}
function renderWizard() {
  const el = $('wizard');
  const w = WIZ || {};
  if (!w.running && (!w.steps || w.steps.length === 0)) {
    // 시작 화면
    const rids = wizardReaderIds();
    if (rids.length === 0) {
      el.innerHTML = '<div class="banner warn">리더 정보를 불러오는 중입니다. 설정에 리더가 없으면 [설정] 탭에서 추가하세요.</div>';
      return;
    }
    const ropts = rids.map(id => '<option value="' + esc(id) + '">' + esc(id) + '</option>').join('');
    const chks = STEP_DEFS.map(([id, t]) =>
      '<label style="margin-right:10px"><input type="checkbox" class="wchk" value="' + id + '"' +
      (id !== '4' ? ' checked' : '') + '> ' + t + '</label>').join('');
    el.innerHTML = '<div class="wsetup">리더 <select id="wReader">' + ropts + '</select>' +
      '<button class="primary" id="wStart">점검 시작</button></div>' +
      '<div style="font-size:13px">' + chks + '</div>' +
      '<div class="wd" style="margin-top:6px;color:var(--sub)">실태그 체크인(3a)은 실제 서버에 기록을 만듭니다 — 시험용 태그를 사용하세요.</div>';
    $('wStart').onclick = startWizard;
    return;
  }
  // 진행/결과
  let html = '';
  (w.steps || []).forEach(s => {
    const icon = {pass:'✅',fail:'❌',warn:'⚠️',running:'⏳',skip:'⏭️',pending:'·'}[s.status] || '·';
    const metrics = s.metrics ? Object.entries(s.metrics).map(([k,v]) => k+': '+v).join(' · ') : '';
    html += '<div class="wstep ' + esc(s.status) + '"><div class="wicon">' + icon + '</div><div class="wbody">' +
      '<div class="wt">' + esc(s.title) + '</div>' +
      (s.detail ? '<div class="wd">' + esc(s.detail) + '</div>' : '') +
      (s.action ? '<div class="wd">👉 ' + esc(s.action) + '</div>' : '') +
      (metrics ? '<div class="wm">' + esc(metrics) + '</div>' : '') +
      '</div></div>';
  });
  el.innerHTML = html + '<div class="wsetup" style="margin-top:8px">' +
    (w.running
      ? (w.awaitTag
          ? '<b>시험용 태그를 리더에 대고</b> <button class="primary" id="wConfirm">스캔 시작</button> <button id="wAbort">중단</button>'
          : '<span class="badge">점검 진행 중…</span> <button id="wAbort">중단</button>')
      : '<button class="primary" id="wReport">리포트 저장</button> <button id="wRestart">다시 점검</button> <span id="wMsg" class="badge" style="display:none"></span>') +
    '</div>';
  if (w.awaitTag) $('wConfirm').onclick = async () => {
    modal('실태그 체크인', '실제 서버에 체크인 기록이 생성됩니다.\n반드시 시험용 태그를 사용하세요. 진행할까요?', [
      ['취소', '', () => api('api/wizard/abort', {})],
      ['진행', 'primary', () => api('api/wizard/confirm', {confirm: true})],
    ]);
  };
  const ab = $('wAbort'); if (ab) ab.onclick = () => api('api/wizard/abort', {});
  const rp = $('wReport'); if (rp) rp.onclick = async () => {
    const j = await api('api/wizard/report', {});
    const m = $('wMsg'); m.style.display = 'inline';
    m.textContent = j.ok ? '저장됨: ' + j.data.path : (j.error?.message || '실패');
  };
  const rs = $('wRestart'); if (rs) rs.onclick = () => { WIZ = {steps: []}; renderWizard(); };
}
function startWizard() {
  const steps = [...document.querySelectorAll('.wchk:checked')].map(c => c.value);
  const readerId = $('wReader').value;
  if (steps.length === 0) return;
  api('api/wizard/start', {readerId, steps, confirm: true}).then(j => {
    if (!j.ok) modal('시작 실패', j.error?.message || '', [['닫기', 'primary', null]]);
  });
}

// ─── 설정 ───
let CFG = null, cfgEditing = false;
async function loadConfig() {
  const j = await api('api/config');
  if (j.ok) { CFG = j.data; renderConfig(); refreshWizardIfIdle(); }
}
function renderConfig() {
  const el = $('cfgView');
  if (!CFG) { el.innerHTML = ''; return; }
  const c = CFG.config;
  if (!cfgEditing) {
    const rlist = (c.readers || []).map(r =>
      '· <b>' + esc(r.id) + '</b> — ' + esc(r.addr) +
      (CFG.tokenSet && CFG.tokenSet[r.id] ? ' <span class="state-ok">토큰 지정됨</span>' : ' <span class="state-warn">토큰 미지정(세션 선택 필요)</span>')
    ).join('<br>');
    el.innerHTML = '<div class="banner info" style="background:#fff;border-color:var(--line);color:var(--txt)">' +
      '서버: ' + esc(c.apiHost) + '<br>데이터 폴더: ' + esc(c.dataDir) +
      '<br>디바운스: ' + c.debounceSec + '초 · 출력: ' + c.powerGain + ' · 부저: ' + (c.buzzer ? '켬' : '끔') +
      ' · 로그: ' + esc(c.logLevel) +
      '<br>세션 파일: ' + esc(c.sessionsFile || '(기본: config 옆 pulse-sessions.json)') +
      '<br><br>리더:<br>' + rlist + '</div>';
    $('cfgEdit').textContent = '편집';
    return;
  }
  // 편집 폼
  const f = (label, key, type) =>
    '<div class="fld"><label>' + label + '</label>' +
    '<input data-k="' + key + '" type="' + (type || 'text') + '" value="' + esc(c[key] ?? '') + '"></div>';
  let html = f('서버 apiHost', 'apiHost') + f('데이터 폴더', 'dataDir') +
    f('디바운스(초)', 'debounceSec', 'number') + f('큐 보관(시간)', 'queueMaxAgeHours', 'number') +
    f('요청 타임아웃(초)', 'requestTimeoutSec', 'number') + f('출력 powerGain', 'powerGain', 'number') +
    '<div class="fld"><label>부저</label><select data-k="buzzer"><option value="0"' + (c.buzzer ? '' : ' selected') + '>끔</option><option value="1"' + (c.buzzer ? ' selected' : '') + '>켬</option></select></div>' +
    '<div class="fld"><label>로그 레벨</label><select data-k="logLevel">' +
      ['debug','info','warn','error'].map(l => '<option' + (c.logLevel === l ? ' selected' : '') + '>' + l + '</option>').join('') +
    '</select></div>' +
    f('세션 파일 경로', 'sessionsFile') +
    '<div style="margin-top:10px"><b>리더</b> <button id="rdrAdd">+ 리더 추가</button></div><div id="rdrList"></div>' +
    '<div style="margin-top:12px"><button class="primary" id="cfgSave">저장 및 적용</button> <button id="cfgCancel">취소</button> <span id="cfgMsg" class="badge" style="display:none"></span></div>';
  el.innerHTML = html;
  $('cfgEdit').textContent = '편집 취소';
  renderReaderRows(c.readers || []);
  el.querySelectorAll('[data-k]').forEach(inp => {
    inp.oninput = () => {
      const k = inp.dataset.k;
      c[k] = (inp.type === 'number' || k === 'buzzer') ? Number(inp.value) : inp.value;
    };
  });
  $('rdrAdd').onclick = () => { c.readers.push({id: 'gate-' + String.fromCharCode(97 + c.readers.length), addr: '192.168.9.6:5578'}); renderReaderRows(c.readers); };
  $('cfgCancel').onclick = () => { cfgEditing = false; loadConfig(); };
  $('cfgSave').onclick = saveConfig;
}
function renderReaderRows(readers) {
  const box = $('rdrList');
  box.innerHTML = '';
  readers.forEach((r, i) => {
    const row = document.createElement('div');
    row.className = 'rdrRow';
    row.innerHTML = '<input placeholder="id" value="' + esc(r.id) + '" data-i="' + i + '" data-f="id">' +
      '<input placeholder="주소 (IP:포트)" value="' + esc(r.addr) + '" data-i="' + i + '" data-f="addr">' +
      '<button data-del="' + i + '">삭제</button>';
    box.appendChild(row);
  });
  box.querySelectorAll('input').forEach(inp => {
    inp.oninput = () => { readers[inp.dataset.i][inp.dataset.f] = inp.value; };
  });
  box.querySelectorAll('[data-del]').forEach(b => {
    b.onclick = () => { readers.splice(Number(b.dataset.del), 1); renderReaderRows(readers); };
  });
}
function saveConfig() {
  modal('설정 저장', '설정을 저장하고 적용할까요?' +
    (META.mode === 'hosting' ? '\n적용을 위해 수집이 잠시 재시작됩니다.' : '\n관측 모드에서는 서비스 재시작이 필요합니다.'), [
    ['취소', '', null],
    ['저장', 'primary', async () => {
      const j = await api('api/config', {config: CFG.config, confirm: true});
      if (j.ok) { cfgEditing = false; ctrlMsg(j.data.message || '저장됨'); loadConfig(); loadCatalog(); }
      else modal('저장 실패', j.error?.message || '', [['닫기', 'primary', null]]);
    }],
  ]);
}
$('cfgEdit').onclick = () => { cfgEditing = !cfgEditing; renderConfig(); };

// ─── 로그 ───
function appendLog(raw) {
  let o;
  try { o = JSON.parse(raw); } catch { o = {event: 'RAW', message: raw, level: 'info'}; }
  logBuf.push(o);
  if (logBuf.length > MAXLOG) logBuf.shift();
  if (!paused) drawLog();
}
function drawLog() {
  const lv = $('fLevel').value, rd = $('fReader').value.trim(), ev = $('fEvent').value.trim();
  const el = $('logs');
  const atBottom = el.scrollTop + el.clientHeight >= el.scrollHeight - 20;
  el.innerHTML = logBuf.filter(o =>
    (!lv || o.level === lv) &&
    (!rd || (o.readerId || '').includes(rd)) &&
    (!ev || (o.event || '').includes(ev))
  ).map(o => {
    const t = (o.ts || '').slice(11, 19);
    const cls = o.level === 'error' ? 'le' : o.level === 'warn' ? 'lw' : o.level === 'debug' ? 'ld' : '';
    const extra = Object.entries(o)
      .filter(([k]) => !['ts','level','event','readerId','message'].includes(k))
      .map(([k, v]) => k + '=' + v).join(' ');
    return '<div class="' + cls + '">' + esc(t) + '  [' + esc(o.level || '?') + '] ' +
      esc(o.event || '') + (o.readerId ? ' (' + esc(o.readerId) + ')' : '') +
      (o.message ? ' — ' + esc(o.message) : '') + (extra ? '  ' + esc(extra) : '') + '</div>';
  }).join('');
  if (atBottom) el.scrollTop = el.scrollHeight;
}
['fLevel', 'fReader', 'fEvent'].forEach(id => $(id).addEventListener('input', drawLog));
$('pause').onclick = () => {
  paused = !paused;
  $('pause').textContent = paused ? '재개' : '일시정지';
  if (!paused) drawLog();
};

// ─── 초기화 ───
fetch('api/meta').then(r => r.json()).then(({data}) => {
  META = data;
  $('ver').textContent = (data.version || '') + (data.cfgFingerprint ? ' · cfg ' + data.cfgFingerprint : '');
  if (data.configError) {
    const el = $('cfgerr');
    el.style.display = 'block';
    el.textContent = '설정 파일을 읽을 수 없습니다.\n' + data.configError +
      '\n\nconfig.example.json 을 복사해 config.json 을 만들고 프로그램을 다시 시작하세요.';
  }
  renderCtrl(LAST_STATE);
  loadCatalog();
  loadConfig();
  loadWizard();
});

function connectSSE() {
  const es = new EventSource('api/events');
  es.addEventListener('state', e => renderState(JSON.parse(e.data)));
  es.addEventListener('log', e => appendLog(e.data));
  es.addEventListener('catalog', () => loadCatalog());
  es.addEventListener('wizard', e => { WIZ = JSON.parse(e.data); renderWizard(); });
  es.onerror = () => { es.close(); setTimeout(connectSSE, 2000); };
}
fetch('api/state').then(r => r.json()).then(({data}) => renderState(data)).catch(() => {});
connectSSE();
