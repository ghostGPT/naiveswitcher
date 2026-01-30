var currentData = {};
var autoSwitchPaused = false;
var countdown = 3;

function toast(msg, type) {
    var m = {ok:'to',err:'te',info:'ti'};
    var c = document.getElementById('toast-container');
    var el = document.createElement('div');
    el.className = 't ' + (m[type] || 'ti');
    el.textContent = msg;
    c.appendChild(el);
    setTimeout(function() {
        el.classList.add('out');
        setTimeout(function() { el.remove(); }, 150);
    }, 2500);
}

function tick() {
    var el = document.getElementById('refresh-countdown');
    if (el) el.textContent = countdown + 's';
    if (countdown <= 0) { countdown = 3; poll(); }
    else countdown--;
}

function poll() {
    fetch('/api/status').then(function(r) { return r.json(); }).then(function(res) {
        if (res.success && res.data) { currentData = res.data; render(); }
    }).catch(function() {});
}

function render() {
    var d = currentData;
    var err = d.error_count || 0;

    setText('current-server', d.current_server || '未知');

    var dot = document.getElementById('server-status-indicator');
    if (dot) dot.className = 'd' + (err > 5 ? ' de' : err > 0 ? ' dw' : '');

    setText('uptime', d.uptime || '--');
    setText('goroutine-count', d.goroutine_count || '--');
    setText('memory-usage', d.memory_usage_mb ? d.memory_usage_mb + ' MB' : '--');
    setText('last-update', new Date().toLocaleTimeString());

    var ecEl = document.getElementById('error-count');
    if (ecEl) {
        ecEl.textContent = err;
        ecEl.className = err > 5 ? 'et' : err > 0 ? 'wt' : 'ok';
    }

    autoSwitchPaused = d.auto_switch_paused || false;
    var asEl = document.getElementById('auto-switch-status');
    if (asEl) {
        asEl.innerHTML = '<span class="d' + (autoSwitchPaused ? ' dw' : '') + '"></span> ' +
            (autoSwitchPaused ? '已暂停' : '运行中');
    }

    var asBtn = document.getElementById('auto-switch-btn');
    if (asBtn) asBtn.textContent = autoSwitchPaused ? '恢复自动' : '暂停自动';

    var nv = d.naive_version || '--';
    if (nv !== '--') {
        var m = nv.match(/(v[\d.]+(?:-\d+)?)-/);
        if (m) nv = m[1];
    }
    setText('naive-version', nv);

    var sv = d.switcher_version || '--';
    if (sv !== '--' && sv.charAt(0) !== 'v') sv = 'v' + sv;
    setText('switcher-version', sv);

    var ds = d.down_stats || {};
    var dsEl = document.getElementById('down-stats');
    if (dsEl) {
        var keys = Object.keys(ds);
        if (keys.length === 0) { dsEl.textContent = '暂无记录'; }
        else {
            var t = '';
            keys.forEach(function(k) { t += k + ': ' + ds[k] + '\n'; });
            dsEl.textContent = t.trim();
        }
    }

    var sel = document.getElementById('server-select');
    if (!sel) return;
    var cur = d.current_server;
    var svrs = d.available_servers || [];
    var prev = sel.value;
    sel.innerHTML = '<option value="">选择服务器…</option>';
    svrs.forEach(function(s) {
        var o = document.createElement('option');
        o.value = s;
        o.textContent = s + (s === cur ? ' ✓' : '');
        if (s === cur) o.disabled = true;
        sel.appendChild(o);
    });
    if (prev && svrs.indexOf(prev) !== -1) sel.value = prev;
    syncBtn();
}

function setText(id, v) {
    var el = document.getElementById(id);
    if (el) el.textContent = v;
}

function syncBtn() {
    var sel = document.getElementById('server-select');
    var btn = document.getElementById('switch-btn');
    btn.disabled = !sel.value || sel.value === currentData.current_server;
}

function switchToBestServer() {
    if (!confirm('切换到最佳服务器？')) return;
    fetch('/api/switch', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ type: 'avoid', avoid_server: currentData.current_server })
    }).then(function(r) { return r.json(); }).then(function(res) {
        if (res.success) { toast('正在切换…', 'ok'); setTimeout(poll, 2000); }
        else toast(res.error || '失败', 'err');
    }).catch(function(e) { toast('出错：' + e.message, 'err'); });
}

function toggleAutoSwitch() {
    var action = autoSwitchPaused ? 'resume' : 'pause';
    fetch('/api/auto-switch', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ action: action })
    }).then(function(r) { return r.json(); }).then(function(res) {
        if (res.success) { toast(autoSwitchPaused ? '已恢复' : '已暂停', 'ok'); poll(); }
        else toast(res.error || '失败', 'err');
    }).catch(function(e) { toast('出错：' + e.message, 'err'); });
}

function checkUpdates() {
    fetch('/api/update', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' }
    }).then(function(r) { return r.json(); }).then(function(res) {
        if (res.success) toast('已开始检查更新', 'info');
        else toast(res.error || '失败', 'err');
    }).catch(function(e) { toast('出错：' + e.message, 'err'); });
}

function viewLogs() {
    document.getElementById('logs-modal').classList.add('active');
    var el = document.getElementById('logs-content');
    el.textContent = '加载中…';
    fetch('/api/logs').then(function(r) { return r.text(); }).then(function(t) {
        el.textContent = t.trim() || '暂无日志';
        el.scrollTop = el.scrollHeight;
    }).catch(function(e) { el.textContent = '加载失败：' + e.message; });
}

function closeLogsModal() {
    document.getElementById('logs-modal').classList.remove('active');
}

function switchToSelectedServer() {
    var s = document.getElementById('server-select').value;
    if (!s || !confirm('切换到 ' + s + ' ？')) return;
    fetch('/api/switch', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ type: 'select', target_server: s })
    }).then(function(r) { return r.json(); }).then(function(res) {
        if (res.success) { toast('正在切换…', 'ok'); setTimeout(poll, 2000); }
        else toast(res.error || '失败', 'err');
    }).catch(function(e) { toast('出错：' + e.message, 'err'); });
}

function toggleTheme() {
    var h = document.documentElement;
    var cur = localStorage.getItem('theme') || 'auto';
    var next = cur === 'auto' ? 'light' : cur === 'light' ? 'dark' : 'auto';
    h.classList.remove('dark', 'light');
    if (next === 'dark') h.classList.add('dark');
    else if (next === 'light') h.classList.add('light');
    localStorage.setItem('theme', next);
    syncThemeBtn(next);
}

function syncThemeBtn(t) {
    var labels = {auto:'◐',light:'☀️',dark:'🌙'};
    document.getElementById('theme-btn').textContent = labels[t] || '◐';
}

document.addEventListener('DOMContentLoaded', function() {
    syncThemeBtn(localStorage.getItem('theme') || 'auto');
    document.getElementById('server-select').addEventListener('change', syncBtn);
    document.getElementById('logs-modal').addEventListener('click', function(e) {
        if (e.target === this) closeLogsModal();
    });
    poll();
    setInterval(tick, 1000);
});
