'use strict';
const $ = id => document.getElementById(id);
const MAXLOG = 2000;
let paused = false;
const logBuf = [];

fetch('api/meta').then(r => r.json()).then(({data}) => {
  $('ver').textContent = (data.version || '') + (data.cfgFingerprint ? ' · cfg ' + data.cfgFingerprint : '');
  if (data.configError) {
    const el = $('cfgerr');
    el.style.display = 'block';
    el.textContent = '설정 파일을 읽을 수 없습니다.\n' + data.configError +
      '\n\nconfig.example.json 을 복사해 config.json 을 만들고 프로그램을 다시 시작하세요.';
  }
});

function fmtTime(s) {
  if (!s) return '—';
  const d = new Date(s);
  return isNaN(d) ? s : d.toLocaleTimeString('ko-KR', {hour12: false});
}

function renderState(st) {
  $('dot').className = st.signal || 'gray';
  $('headline').textContent = st.headline || '';
  $('mode').textContent = st.coreRunning
    ? (st.coreMode === 'service' ? '서비스 동작 중' : '코어 동작 중 (' + (st.coreMode || '?') + ')')
    : '관측 모드';
  const rs = $('readers');
  rs.innerHTML = '';
  (st.readers || []).forEach(r => {
    const cls = r.gateState === 'ACTIVE' ? 'state-ok'
      : (r.gateState || '').startsWith('SUSPENDED') ? 'state-bad' : 'state-warn';
    const conn = r.connState === 'CONNECTED'
      ? '<span class="state-ok">연결됨</span>' : '<span class="state-bad">끊김</span>';
    const card = document.createElement('div');
    card.className = 'card';
    card.innerHTML =
      '<div class="rid">' + esc(r.id) + '</div>' +
      '<div class="sess">' + esc(r.boothName || r.sessionId || '세션 미지정') +
        (r.unitName ? ' · ' + esc(r.unitName) : '') + '</div>' +
      '<dl class="kv">' +
      '<dt>리더 연결</dt><dd>' + conn + '</dd>' +
      '<dt>전송 상태</dt><dd class="' + cls + '">' + esc(r.gateText || r.gateState) + '</dd>' +
      '<dt>마지막 태그</dt><dd>' + fmtTime(r.lastTagAt) + '</dd>' +
      '<dt>마지막 성공</dt><dd>' + fmtTime(r.lastSuccessAt) + '</dd>' +
      '<dt>대기 큐</dt><dd>' + (r.pending || 0) + '건</dd>' +
      '</dl>' +
      (r.actionText ? '<div class="action">👉 ' + esc(r.actionText) + '</div>' : '');
    rs.appendChild(card);
  });
  $('queue').textContent = st.coreRunning
    ? '전체 미전송 ' + (st.queueDepth || 0) + '건 · 상태 갱신 ' + fmtTime(st.updatedAt) +
      (st.ntp && st.ntp.checked ? ' · 시계 오차 ' + st.ntp.skewSec + '초' : '')
    : '';
}

function esc(s) {
  return String(s ?? '').replace(/[&<>"']/g, c =>
    ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
}

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

async function svc(action, label) {
  if (!confirm('서비스를 ' + label + '할까요?' + (action === 'stop' ? '\n중지하면 체크인 수집이 멈춥니다.' : ''))) return;
  const m = $('svcMsg');
  m.style.display = 'inline';
  m.textContent = label + ' 요청 중…';
  try {
    const r = await fetch('api/control/service', {
      method: 'POST', headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({action, confirm: true}),
    });
    const j = await r.json();
    m.textContent = j.ok ? label + ' 요청 완료 — 잠시 후 상태가 갱신됩니다' : (j.error?.message || '실패');
  } catch (e) {
    m.textContent = '실패: ' + e;
  }
}
$('svcStart').onclick = () => svc('start', '시작');
$('svcStop').onclick = () => svc('stop', '중지');

function connectSSE() {
  const es = new EventSource('api/events');
  es.addEventListener('state', e => renderState(JSON.parse(e.data)));
  es.addEventListener('log', e => appendLog(e.data));
  es.onerror = () => {
    es.close();
    setTimeout(connectSSE, 2000);
  };
}
fetch('api/state').then(r => r.json()).then(({data}) => renderState(data)).catch(() => {});
connectSSE();
