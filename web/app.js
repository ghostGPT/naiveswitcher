let currentData = {};
let autoSwitchPaused = false;
let countdown = 3;
let countdownTimer = null;

// Update countdown display
function updateCountdown() {
    const countdownEl = document.getElementById('refresh-countdown');
    if (countdownEl) {
        countdownEl.textContent = countdown + '秒';
    }

    if (countdown <= 0) {
        countdown = 3;
        fetchStatus();
    } else {
        countdown--;
    }
}

// Fetch status from API
async function fetchStatus() {
    try {
        const response = await fetch('/api/status');
        const result = await response.json();

        if (result.success && result.data) {
            currentData = result.data;
            updateUI();
        }
    } catch (error) {
        console.error('Error fetching status:', error);
    }
}

// Update UI with current data
function updateUI() {
    const data = currentData || {};

    // Current server
    const currentServerEl = document.getElementById('current-server');
    if (currentServerEl) {
        currentServerEl.textContent = data.current_server || '未知';
    }

    // Status indicator
    const indicator = document.getElementById('server-status-indicator');
    const errorCount = data.error_count || 0;
    if (indicator) {
        indicator.className = 'status-indicator ' +
            (errorCount > 5 ? 'error' : errorCount > 0 ? 'warning' : 'online');
    }

    // Uptime
    const uptimeEl = document.getElementById('uptime');
    if (uptimeEl) {
        uptimeEl.textContent = data.uptime || '--';
    }

    // Auto switch status
    autoSwitchPaused = data.auto_switch_paused || false;
    const autoSwitchStatus = document.getElementById('auto-switch-status');
    if (autoSwitchStatus) {
        autoSwitchStatus.innerHTML = `
            <span class="status-indicator ${autoSwitchPaused ? 'warning' : 'online'}"></span>
            <span class="${autoSwitchPaused ? 'warning-text' : 'success-text'}">
                ${autoSwitchPaused ? '已暂停' : '运行中'}
            </span>
        `;
    }

    // Update button text
    const autoSwitchBtn = document.getElementById('auto-switch-btn');
    if (autoSwitchBtn) {
        autoSwitchBtn.textContent = autoSwitchPaused ? '▶️ 恢复自动切换' : '⏸️ 暂停自动切换';
        autoSwitchBtn.className = autoSwitchPaused ? 'btn success' : 'btn secondary';
    }

    // Error count
    const errorCountEl = document.getElementById('error-count');
    if (errorCountEl) {
        errorCountEl.textContent = errorCount;
        errorCountEl.className = 'metric-value ' +
            (errorCount > 5 ? 'error-text' : errorCount > 0 ? 'warning-text' : 'success-text');
    }

    // Goroutine count
    const goroutineEl = document.getElementById('goroutine-count');
    if (goroutineEl) {
        goroutineEl.textContent = data.goroutine_count || '--';
    }

    // Memory usage
    const memoryUsageEl = document.getElementById('memory-usage');
    if (memoryUsageEl) {
        if (data.memory_usage_mb) {
            memoryUsageEl.textContent = data.memory_usage_mb + ' MB';
        } else {
            memoryUsageEl.textContent = '--';
        }
    }

    // Versions
    const naiveVersionEl = document.getElementById('naive-version');
    if (naiveVersionEl) {
        const naiveVersion = data.naive_version || '--';
        if (naiveVersion !== '--') {
            const match = naiveVersion.match(/(v[\d.]+(?:-\d+)?)-/);
            naiveVersionEl.textContent = match ? match[1] : naiveVersion;
        } else {
            naiveVersionEl.textContent = naiveVersion;
        }
    }

    const switcherVersionEl = document.getElementById('switcher-version');
    if (switcherVersionEl) {
        const switcherVersion = data.switcher_version || '--';
        // Add 'v' prefix if not present and it's a valid version number
        if (switcherVersion !== '--' && !switcherVersion.startsWith('v')) {
            switcherVersionEl.textContent = 'v' + switcherVersion;
        } else {
            switcherVersionEl.textContent = switcherVersion;
        }
    }

    // Last update time
    const lastUpdateEl = document.getElementById('last-update');
    if (lastUpdateEl) {
        lastUpdateEl.textContent = new Date().toLocaleTimeString();
    }

    // Down stats
    const downStatsEl = document.getElementById('down-stats');
    if (downStatsEl) {
        const downStats = data.down_stats || {};
        let downStatsText = '';
        if (Object.keys(downStats).length === 0) {
            downStatsText = '未记录到服务器故障';
        } else {
            for (const [server, count] of Object.entries(downStats)) {
                downStatsText += `${server}: ${count}\n`;
            }
        }
        downStatsEl.textContent = downStatsText;
    }

    // Available servers
    const serverSelect = document.getElementById('server-select');
    if (!serverSelect) return;
    const currentServer = data.current_server;
    const servers = data.available_servers || [];

    // Preserve current selection
    const currentSelection = serverSelect.value;

    serverSelect.innerHTML = '<option value="">-- 选择要切换的服务器 --</option>';
    servers.forEach(server => {
        const option = document.createElement('option');
        option.value = server;
        option.textContent = server + (server === currentServer ? ' (当前)' : '');
        if (server === currentServer) {
            option.disabled = true;
        }
        serverSelect.appendChild(option);
    });

    // Restore selection if still valid
    if (currentSelection && servers.includes(currentSelection)) {
        serverSelect.value = currentSelection;
    }

    updateSwitchButton();
}

// Switch to best server
async function switchToBestServer() {
    if (!confirm('切换到最佳可用服务器？')) return;

    try {
        const response = await fetch('/api/switch', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                type: 'avoid',
                avoid_server: currentData.current_server
            })
        });

        const result = await response.json();
        if (result.success) {
            alert('正在切换到最佳服务器...');
            setTimeout(fetchStatus, 2000);
        } else {
            alert('错误：' + (result.error || '未知错误'));
        }
    } catch (error) {
        alert('切换服务器时出错：' + error.message);
    }
}

// Toggle auto switch
async function toggleAutoSwitch() {
    const action = autoSwitchPaused ? 'resume' : 'pause';

    try {
        const response = await fetch('/api/auto-switch', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ action })
        });

        const result = await response.json();
        if (result.success) {
            fetchStatus();
        } else {
            alert('错误：' + (result.error || '未知错误'));
        }
    } catch (error) {
        alert('切换自动开关时出错：' + error.message);
    }
}

// Check for updates
async function checkUpdates() {
    try {
        const response = await fetch('/api/update', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' }
        });

        const result = await response.json();
        if (result.success) {
            alert('已开始检查更新，请查看日志了解结果。');
        } else {
            alert('错误：' + (result.error || '未知错误'));
        }
    } catch (error) {
        alert('检查更新时出错：' + error.message);
    }
}

// View logs
function viewLogs() {
    const modal = document.getElementById('logs-modal');
    modal.classList.add('active');
    loadLogs();
}

// Close logs modal
function closeLogsModal() {
    document.getElementById('logs-modal').classList.remove('active');
}

// Load logs
async function loadLogs() {
    const logsContent = document.getElementById('logs-content');
    logsContent.innerHTML = '<div class="loading">加载日志中...</div>';

    try {
        const response = await fetch('/api/logs');
        const logs = await response.text();

        if (logs.trim() === '') {
            logsContent.innerHTML = '<div class="loading">暂无日志</div>';
        } else {
            logsContent.textContent = logs;
            logsContent.scrollTop = logsContent.scrollHeight;
        }
    } catch (error) {
        logsContent.innerHTML = '<div class="loading error-text">加载日志时出错：' + error.message + '</div>';
    }
}

// Update switch button state
function updateSwitchButton() {
    const select = document.getElementById('server-select');
    const btn = document.getElementById('switch-btn');
    const selectedServer = select.value;

    btn.disabled = !selectedServer || selectedServer === currentData.current_server;
}

// Switch to selected server
async function switchToSelectedServer() {
    const select = document.getElementById('server-select');
    const selectedServer = select.value;

    if (!selectedServer) return;
    if (!confirm('切换到：' + selectedServer + '？')) return;

    try {
        const response = await fetch('/api/switch', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                type: 'select',
                target_server: selectedServer
            })
        });

        const result = await response.json();
        if (result.success) {
            alert('正在切换到所选服务器...');
            setTimeout(fetchStatus, 2000);
        } else {
            alert('错误：' + (result.error || '未知错误'));
        }
    } catch (error) {
        alert('切换服务器时出错：' + error.message);
    }
}

// Event listeners
document.addEventListener('DOMContentLoaded', function() {
    // Server select change event
    document.getElementById('server-select').addEventListener('change', updateSwitchButton);

    // Close modal when clicking outside
    document.getElementById('logs-modal').addEventListener('click', function (e) {
        if (e.target === this) {
            closeLogsModal();
        }
    });

    // Initialize
    fetchStatus();

    // Start countdown timer (update every second)
    countdownTimer = setInterval(updateCountdown, 1000);
});
