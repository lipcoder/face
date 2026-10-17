const canvas = document.getElementById('view'), ctx = canvas.getContext('2d'), hint = document.getElementById('hint');
let polling = false;
async function updateFrame() {
  if (polling) return; polling = true;
  try {
    const response = await fetch('/api/state', { cache: 'no-store' }), state = await response.json();
    if (state.image) {
      const image = new Image(); image.onload = () => {
        canvas.width = image.width; canvas.height = image.height; ctx.drawImage(image, 0, 0);
        for (const face of state.faces || []) {
          const box = face.Box, x = box.X * canvas.width, y = box.Y * canvas.height, w = box.Width * canvas.width, h = box.Height * canvas.height;
          ctx.strokeStyle = '#60e69e'; ctx.lineWidth = 3; ctx.strokeRect(x, y, w, h); ctx.font = '20px system-ui'; ctx.fillStyle = '#60e69e'; ctx.fillText(face.Name || '未识别', x, Math.max(22, y - 8));
        } hint.textContent = '向下滚动查看本周签到人员';
      }; image.src = state.image;
    }
  } catch (error) { hint.textContent = '画面连接失败：' + error.message } finally { polling = false }
}
async function updateAttendance() {
  try {
    const response = await fetch('/api/attendance?week=current', { cache: 'no-store' }); if (!response.ok) throw Error(await response.text());
    const { week_start, people, records } = await response.json();
    const days = Array.from({ length: 7 }, (_, offset) => {
      const date = new Date(week_start + 'T00:00:00Z');
      date.setUTCDate(date.getUTCDate() + offset);
      return date.toISOString().slice(0, 10);
    });
    document.getElementById('week').textContent = days[0] + ' — ' + days[6] + ' · ✓ 已签到';
    const head = document.getElementById('attendance-head');
    head.replaceChildren();
    for (const label of ['学号', '姓名', ...days.map((day, i) => ['周一', '周二', '周三', '周四', '周五', '周六', '周日'][i] + ' ' + day.slice(5))]) {
      const cell = document.createElement('th'); cell.textContent = label; head.append(cell);
    }
    const signedDays = new Set(records.map(record => record.person_id + ':' + record.sign_date));
    const rows = document.getElementById('rows'); rows.replaceChildren();
    document.getElementById('empty').textContent = people.length ? '' : '尚未添加人员';
    for (const person of people) {
      const row = document.createElement('tr');
      for (const value of [person.person_id, person.name]) {
        const cell = document.createElement('td'); cell.textContent = value; row.append(cell);
      }
      for (const day of days) {
        const cell = document.createElement('td');
        const signed = signedDays.has(person.person_id + ':' + day);
        cell.textContent = signed ? '✓' : '—';
        cell.className = signed ? 'signed' : 'unsigned';
        cell.setAttribute('aria-label', day + (signed ? ' 已签到' : ' 未签到'));
        row.append(cell);
      }
      rows.append(row);
    }
    const logs = document.getElementById('logs'); logs.replaceChildren();
    document.getElementById('logs-empty').textContent = records.length ? '' : '本周暂无签到日志';
    for (const record of records) {
      const row = document.createElement('tr');
      const time = new Date(record.signed_at).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai', hour12: false });
      for (const value of [time, record.person_id, record.name || '已删除人员']) {
        const cell = document.createElement('td'); cell.textContent = value; row.append(cell);
      }
      logs.append(row);
    }
  } catch (error) { document.getElementById('empty').textContent = '读取签到记录失败：' + error.message }
}
setInterval(updateFrame, 250); updateFrame(); setInterval(updateAttendance, 10000); updateAttendance();
