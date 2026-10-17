const message = document.getElementById('message');
let captureSession = null;
let enrollmentController = null;
let sourceInfo = null;

function stopEnrollment() {
  const session = captureSession;
  captureSession = null;
  if (session) {
    session.controller.abort();
    if (session.stream) session.stream.getTracks().forEach(track => track.stop());
    const video = document.getElementById('capture');
    if (session.callbackID !== null) video.cancelVideoFrameCallback(session.callbackID);
    if (session.timerID !== null) clearInterval(session.timerID);
    video.pause();
    video.srcObject = null;
  }
  if (enrollmentController) enrollmentController.abort();
  document.getElementById('preview').removeAttribute('src');
  document.getElementById('enroll').hidden = true;
  document.getElementById('start').disabled = false;
  document.getElementById('stop').hidden = true;
  message.textContent = '添加人脸已关闭，摄像头已释放';
}

async function detectFrameRate(video, track) {
  const rate = track.getSettings().frameRate;
  if (Number.isFinite(rate) && rate > 0) return rate;
  if (!video.requestVideoFrameCallback) throw Error('浏览器未提供摄像头帧率');
  return new Promise((resolve, reject) => {
    let first, count = 0, done = false;
    const timeout = setTimeout(() => { done = true; reject(Error('无法检测摄像头帧率')) }, 3000);
    function sample(now, meta) {
      if (done) return;
      if (first === undefined) first = meta.mediaTime;
      count++;
      if (count >= 12 && meta.mediaTime > first) { done = true; clearTimeout(timeout); resolve((count - 1) / (meta.mediaTime - first)); return }
      video.requestVideoFrameCallback(sample);
    }
    video.requestVideoFrameCallback(sample);
  });
}
async function startEnrollment() {
  const session = { controller: new AbortController(), stream: null, callbackID: null, timerID: null };
  captureSession = session;
  document.getElementById('start').disabled = true;
  document.getElementById('stop').hidden = false;
  try {
    if (!window.isSecureContext) throw Error('远程管理摄像头需要 HTTPS，请使用证书启动管理端口');
    const video = document.getElementById('capture');
    const constraints = sourceInfo ? {
      width: { exact: sourceInfo.width }, height: { exact: sourceInfo.height }, frameRate: { exact: sourceInfo.framerate }
    } : true;
    const stream = await navigator.mediaDevices.getUserMedia({ audio: false, video: constraints });
    if (captureSession !== session) { stream.getTracks().forEach(track => track.stop()); return }
    session.stream = stream;
    video.srcObject = stream; await video.play();
    const track = stream.getVideoTracks()[0];
    const detectedRate = await detectFrameRate(video, track);
    const sourceWidth = video.videoWidth, sourceHeight = video.videoHeight;
    const info = { width: sourceWidth, height: sourceHeight, framerate: detectedRate };
    const configured = await fetch('/api/source/config', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(info), signal: session.controller.signal });
    if (!configured.ok) throw Error(await configured.text());
    if (captureSession !== session) return;
    sourceInfo = info;
    const input = document.createElement('canvas'), inputCtx = input.getContext('2d');
    input.width = sourceWidth; input.height = sourceHeight;
    let uploading = false, shown = false;
    async function sendFrame() {
      if (captureSession !== session || uploading || video.readyState < 2) return;
      uploading = true;
      try {
        inputCtx.drawImage(video, 0, 0);
        const blob = await new Promise(resolve => input.toBlob(resolve, 'image/jpeg', 0.75));
        if (captureSession !== session) return;
        if (!blob) throw Error('无法编码摄像头画面');
        const response = await fetch('/api/source/frame', { method: 'POST', headers: { 'Content-Type': 'image/jpeg' }, body: blob, signal: session.controller.signal });
        if (!response.ok) throw Error(await response.text());
        if (captureSession !== session) return;
        if (!shown) { shown = true; document.getElementById('preview').src = '/api/preview'; document.getElementById('enroll').hidden = false; message.textContent = '请面对摄像头，然后填写学号和姓名'; }
      } catch (error) { if (captureSession === session) message.textContent = '上传画面失败：' + error.message }
      finally { uploading = false }
    }
    if (video.requestVideoFrameCallback) {
      function onFrame() {
        if (captureSession !== session) return;
        session.callbackID = video.requestVideoFrameCallback(onFrame);
        sendFrame();
      }
      session.callbackID = video.requestVideoFrameCallback(onFrame);
    } else session.timerID = setInterval(sendFrame, 1000 / detectedRate);
  } catch (error) {
    if (captureSession !== session) return;
    stopEnrollment();
    message.textContent = '无法打开浏览器摄像头：' + error.message;
  }
}
document.getElementById('start').addEventListener('click', startEnrollment);
document.getElementById('stop').addEventListener('click', stopEnrollment);
window.addEventListener('beforeunload', stopEnrollment);
async function loadPeople() {
  const response = await fetch('/api/people'); if (!response.ok) throw Error(await response.text());
  const body = document.getElementById('people'); body.replaceChildren();
  for (const person of await response.json()) {
    const row = document.createElement('tr');
    for (const value of [person.person_id, person.name]) { const cell = document.createElement('td'); cell.textContent = value; row.append(cell) }
    const cell = document.createElement('td'), rename = document.createElement('button'), remove = document.createElement('button');
    rename.textContent = '改名'; remove.textContent = '删除';
    rename.onclick = async () => { const name = prompt('新姓名', person.name); if (!name) return; const result = await fetch('/api/people/' + person.person_id, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name }) }); if (!result.ok) { message.textContent = await result.text(); return } loadPeople() };
    remove.onclick = async () => { if (!confirm('删除 ' + person.name + '？')) return; const result = await fetch('/api/people/' + person.person_id, { method: 'DELETE' }); if (!result.ok) { message.textContent = await result.text(); return } loadPeople() };
    cell.append(rename, remove); row.append(cell); body.append(row);
  }
}
loadPeople();
document.getElementById('form').addEventListener('submit', async e => {
  if (!captureSession || enrollmentController) { e.preventDefault(); return }
  const controller = new AbortController();
  enrollmentController = controller;
  e.preventDefault(); const button = e.target.querySelector('button'); button.disabled = true; message.textContent = '正在采集人脸，请独自面对摄像头…';
  try { const res = await fetch('/api/people', { method: 'POST', body: new URLSearchParams(new FormData(e.target)), signal: controller.signal }); if (!res.ok) throw Error(await res.text()); if (!controller.signal.aborted) { message.textContent = '添加成功'; e.target.reset(); loadPeople(); } }
  catch (err) { if (!controller.signal.aborted) message.textContent = err.message } finally { button.disabled = false; enrollmentController = null }
});
document.getElementById('refresh').addEventListener('click', async () => {
  try { const response = await fetch('/api/refresh', { method: 'POST' }); if (!response.ok) throw Error(await response.text()); message.textContent = '已请求刷新，下一帧将重新识别'; }
  catch (error) { message.textContent = '刷新失败：' + error.message }
});
