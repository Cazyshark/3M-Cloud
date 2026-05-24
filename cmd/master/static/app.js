// Multi-Ops Dashboard - Complete Application
let token = localStorage.getItem('multi_ops_token') || '';
let machines = {};
let currentTerminal = null;
let term = null;
let fitAddon = null;
let _reconnectAttempts = 0;

// ============ Toast Notification System ============
const Toast = {
    _container: null,
    _queue: [],

    init() {
        this._container = document.getElementById('toast-container');
    },

    show(message, type = 'info', duration = 4000) {
        if (!this._container) this.init();

        const icons = { success: '✓', error: '✗', warning: '⚠', info: 'ℹ' };
        const toast = document.createElement('div');
        toast.className = `toast toast-${type}`;
        toast.innerHTML = `<span class="toast-icon">${icons[type] || icons.info}</span><span>${escapeHtml(message)}</span>`;
        this._container.appendChild(toast);

        const remove = () => {
            toast.classList.add('toast-exit');
            setTimeout(() => toast.remove(), 300);
        };
        if (duration > 0) setTimeout(remove, duration);
        return remove;
    },

    success(msg, duration = 3000) { return this.show(msg, 'success', duration); },
    error(msg, duration = 6000) { return this.show(msg, 'error', duration); },
    warning(msg, duration = 4000) { return this.show(msg, 'warning', duration); },
    info(msg, duration = 3000) { return this.show(msg, 'info', duration); },
};

// ============ Copy to Clipboard ============
function copyToClipboard(elementId) {
    const text = document.getElementById(elementId).textContent;
    navigator.clipboard.writeText(text).then(
        () => Toast.success('已复制到剪贴板'),
        () => Toast.error('复制失败')
    );
}

// ============ Auth ============
function handleLogin(e) {
    e.preventDefault();
    const username = document.getElementById('login-user').value;
    const password = document.getElementById('login-pass').value;
    const totp = document.getElementById('login-totp').value;
    const btn = document.getElementById('login-btn');

    btn.disabled = true;
    btn.innerHTML = '<span class="spinner"></span> 登录中...';

    fetch('/api/login', {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({username, password, totp_code: totp})
    })
    .then(r => r.json())
    .then(data => {
        if (data.token) {
            token = data.token;
            localStorage.setItem('multi_ops_token', token);
            Toast.success('登录成功');
            showApp();
        } else {
            const errEl = document.getElementById('login-error');
            if (data.error && data.error.includes('TOTP')) {
                document.getElementById('totp-group').style.display = 'block';
                errEl.textContent = '请输入 TOTP 验证码';
            } else {
                errEl.textContent = data.error || '登录失败';
                Toast.error(data.error || '登录失败');
            }
        }
    })
    .catch(() => {
        document.getElementById('login-error').textContent = '网络错误，请重试';
        Toast.error('网络错误');
    })
    .finally(() => {
        btn.disabled = false;
        btn.textContent = '登录';
    });
}

function handleLogout() {
    token = '';
    localStorage.removeItem('multi_ops_token');
    if (window._dashboardWS) window._dashboardWS.close();
    showLogin();
    Toast.info('已退出登录');
}

function showLogin() {
    document.getElementById('login-page').style.display = 'flex';
    document.getElementById('app-page').style.display = 'none';
}

function showApp() {
    document.getElementById('login-page').style.display = 'none';
    document.getElementById('app-page').style.display = 'flex';
    connectWS();
    refreshMachines();
    refreshHistory();
    loadScriptTemplates();
}

function authHeaders() {
    return {'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'};
}

function showSetupTOTP() {
    fetch('/api/setup-totp', {headers: authHeaders()})
    .then(r => r.json())
    .then(data => {
        if (data.secret) {
            document.getElementById('totp-secret').textContent = data.secret;
            document.getElementById('totp-url').textContent = data.otpauth_url;
            document.getElementById('totp-modal').style.display = 'flex';
        }
    })
    .catch(() => Toast.error('TOTP 设置请求失败'));
}

function closeTOTPModal() {
    document.getElementById('totp-modal').style.display = 'none';
}

// ============ WebSocket ============
function connectWS() {
    _reconnectAttempts = 0;
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const url = `${proto}//${location.host}/ws/dashboard?token=${encodeURIComponent(token)}`;
    const socket = new WebSocket(url);

    socket.onopen = () => {
        _reconnectAttempts = 0;
        const statusEl = document.getElementById('ws-status');
        statusEl.innerHTML = '已连接';
        statusEl.style.color = 'var(--green)';
        socket.send(JSON.stringify({type: 'subscribe'}));
        Toast.success('WebSocket 已连接', 2000);
    };

    socket.onclose = () => {
        _reconnectAttempts++;
        const statusEl = document.getElementById('ws-status');
        statusEl.innerHTML = '<span class="spinner"></span> 断开';
        statusEl.style.color = 'var(--red)';
        if (token) {
            const delay = Math.min(3000 * Math.pow(1.5, _reconnectAttempts - 1), 30000);
            if (_reconnectAttempts <= 1) Toast.warning('WebSocket 断开，正在重连...', 0);
            setTimeout(() => {
                if (token) connectWS();
            }, delay);
        }
    };

    socket.onerror = () => socket.close();

    socket.onmessage = (event) => handleMessage(JSON.parse(event.data));
    window._dashboardWS = socket;
}

function handleMessage(msg) {
    switch (msg.type) {
        case 'machine_list':
        case 'machine_update':
            if (msg.data) {
                if (Array.isArray(msg.data)) {
                    msg.data.forEach(m => { machines[m.agent_id] = m; });
                } else {
                    machines[msg.data.agent_id] = msg.data;
                }
                renderMachineList();
                updateStats();
                updateExecTargets();
                updateUploadTargets();
                updateTerminalSelect();
            }
            break;
        case 'terminal_output':
            if (msg.data && currentTerminal === `${msg.agent_id}:${msg.data.session_id}`) {
                if (term) term.write(msg.data.data);
            }
            break;
        case 'exec_response':
            addExecResult(msg.agent_id, msg.data);
            break;
        case 'file_upload_resp':
            addUploadResult(msg.agent_id, msg.data);
            break;
        case 'metrics':
            if (msg.agent_id && machines[msg.agent_id]) {
                machines[msg.agent_id].metrics = msg.data;
                updateMachineMetrics(msg.agent_id);
            }
            break;
        case 'file_download_resp':
            handleFileDownloadResp(msg.data);
            break;
    }
}

// ============ Machine List ============
function renderMachineList() {
    const container = document.getElementById('machine-list');
    const filter = document.getElementById('machine-filter').value.toLowerCase();
    const groupFilter = document.getElementById('group-filter').value;
    const list = Object.values(machines);

    // Update group filter options
    const groups = new Set();
    list.forEach(m => { if (m.group) groups.add(m.group); });
    const gf = document.getElementById('group-filter');
    const curGroup = gf.value;
    gf.innerHTML = '<option value="">全部分组</option>';
    groups.forEach(g => {
        const opt = document.createElement('option');
        opt.value = g; opt.textContent = g;
        gf.appendChild(opt);
    });
    gf.value = curGroup;

    if (list.length === 0) {
        container.innerHTML = '<div class="empty-state"><div class="empty-state-icon">&#x1F5A5;</div>等待机器连接...</div>';
        return;
    }

    list.sort((a, b) => {
        if (a.status !== b.status) return a.status === 'online' ? -1 : 1;
        return (a.hostname || '').localeCompare(b.hostname || '');
    });

    let html = '';
    let visibleCount = 0;
    for (const m of list) {
        if (groupFilter && m.group !== groupFilter) continue;
        if (filter && !(m.hostname || '').toLowerCase().includes(filter) &&
            !(m.public_ip || '').toLowerCase().includes(filter) &&
            !(m.agent_id || '').toLowerCase().includes(filter) &&
            !(m.tags || []).some(t => t.toLowerCase().includes(filter))) continue;

        visibleCount++;
        const statusClass = m.status === 'online' ? 'online' : 'offline';
        const location = m.location || '未知位置';
        const ip = m.public_ip || 'N/A';
        const os = m.distributor_id ? `${m.distributor_id} ${m.release || ''}` : 'Linux';
        const mem = m.memory_mb ? `${(m.memory_mb / 1024).toFixed(1)}GB` : '';
        const cpu = m.cpu_cores ? `${m.cpu_cores}核` : '';

        // Real-time metrics bar
        let metricsHtml = '';
        if (m.metrics) {
            const cpuPct = m.metrics.cpu_percent?.toFixed(1) || 0;
            const memPct = m.metrics.mem_percent?.toFixed(1) || 0;
            const cpuColor = cpuPct > 80 ? 'var(--red)' : cpuPct > 50 ? 'var(--yellow)' : 'var(--green)';
            const memColor = memPct > 80 ? 'var(--red)' : memPct > 50 ? 'var(--yellow)' : 'var(--green)';
            const load1 = escapeHtml(String(m.metrics.load1?.toFixed(2) || '-'));
            metricsHtml = `
            <div class="metrics-bar">
                <div class="metric"><span>CPU</span><div class="metric-track"><div class="metric-fill" style="width:${cpuPct}%;background:${cpuColor}"></div></div><span class="metric-val">${cpuPct}%</span></div>
                <div class="metric"><span>MEM</span><div class="metric-track"><div class="metric-fill" style="width:${memPct}%;background:${memColor}"></div></div><span class="metric-val">${memPct}%</span></div>
                <div class="metric"><span>Load</span><span class="metric-val">${load1}</span></div>
            </div>`;
        }

        let tagsHtml = '';
        if (m.group) tagsHtml += `<span class="tag tag-group">${escapeHtml(m.group)}</span>`;
        if (m.tags && m.tags.length) {
            m.tags.forEach(t => { tagsHtml += `<span class="tag">${escapeHtml(t)}</span>`; });
        }

        const safeHostname = escapeHtml(m.hostname || m.agent_id);
        const safeIp = escapeHtml(ip);
        const safeLocation = escapeHtml(location);
        const safeOs = escapeHtml(os);
        const safeCpu = escapeHtml(cpu);
        const safeMem = escapeHtml(mem);

        // Search highlight
        const displayName = filter
            ? highlightText(m.hostname || m.agent_id, filter)
            : safeHostname;

        html += `
        <div class="machine-card fade-in" onclick="selectMachine('${m.agent_id}')" id="mc-${m.agent_id}">
            <div class="status-dot ${statusClass}"></div>
            <div class="machine-info">
                <div class="machine-name">${displayName}</div>
                <div class="machine-meta">${safeIp} \xb7 ${safeLocation}</div>
                <div class="machine-meta">${safeOs} \xb7 ${safeCpu} \xb7 ${safeMem}</div>
                ${metricsHtml}
                ${tagsHtml ? `<div class="machine-tags">${tagsHtml}</div>` : ''}
            </div>
            <div class="machine-actions">
                <button class="btn btn-sm" onclick="event.stopPropagation(); openTerminalFor('${m.agent_id}')" title="打开终端">终端</button>
            </div>
        </div>`;
    }

    container.innerHTML = html || '<div class="empty-state">无匹配机器</div>';
}

function highlightText(text, query) {
    const safe = escapeHtml(text);
    const idx = text.toLowerCase().indexOf(query);
    if (idx === -1) return safe;
    const before = safe.substring(0, idx);
    const match = safe.substring(idx, idx + query.length);
    const after = safe.substring(idx + query.length);
    return `${before}<mark style="background:rgba(245,158,11,0.3);color:var(--yellow);border-radius:2px;padding:0 1px">${match}</mark>${after}`;
}

function updateStats() {
    const list = Object.values(machines);
    document.getElementById('online-count').textContent = list.filter(m => m.status === 'online').length;
    document.getElementById('total-count').textContent = list.length;
}

function selectMachine(agentId) {
    document.querySelectorAll('.machine-card').forEach(el => el.classList.remove('selected'));
    const el = document.getElementById(`mc-${agentId}`);
    if (el) el.classList.add('selected');
    showMachineInfo(agentId);
    switchTab('info');
}

function showMachineInfo(agentId) {
    const m = machines[agentId];
    if (!m) return;
    const container = document.getElementById('machine-info');
    const formatUptime = (s) => {
        if (!s) return 'N/A';
        return `${Math.floor(s / 86400)}天 ${Math.floor((s % 86400) / 3600)}小时`;
    };

    let tagsHtml = '';
    (m.tags || []).forEach(t => { tagsHtml += `<span class="tag">${escapeHtml(t)}</span>`; });

    const safeHostname = escapeHtml(m.hostname || m.agent_id);
    const safeGroup = m.group ? escapeHtml(m.group) : '';
    const safeDistributorId = escapeHtml(m.distributor_id || 'N/A');
    const safeDescription = escapeHtml(m.description || 'N/A');
    const safeRelease = escapeHtml(m.release || 'N/A');
    const safeCodename = escapeHtml(m.codename || 'N/A');
    const safePublicIp = escapeHtml(m.public_ip || 'N/A');
    const safeLocation = escapeHtml(m.location || 'N/A');
    const safeCpuModel = escapeHtml(m.cpu_model || 'N/A');
    const safeCpuCores = escapeHtml(String(m.cpu_cores || 'N/A'));
    const safeMem = m.memory_mb ? (m.memory_mb / 1024).toFixed(1) + ' GB' : 'N/A';
    const safeDisk = m.disk_gb ? m.disk_gb + ' GB (已用 ' + m.disk_used + ' GB)' : 'N/A';
    const safeAgentId = escapeHtml(m.agent_id);
    const statusText = m.status === 'online' ? '在线' : '离线';
    const statusColor = m.status === 'online' ? '#22c55e' : '#ef4444';
    const lastSeenText = m.last_seen ? new Date(m.last_seen * 1000).toLocaleString('zh-CN') : 'N/A';

    container.innerHTML = `
    <h2 style="margin-bottom:16px;font-weight:700">${safeHostname}</h2>
    ${safeGroup ? `<p style="margin-bottom:12px"><span class="tag tag-group" style="font-size:13px">${safeGroup}</span></p>` : ''}
    ${m.metrics ? `<div style="margin-bottom:16px" class="info-card"><h4>实时指标</h4><div class="metrics-live">${renderLiveMetrics(m.metrics)}</div></div>` : ''}
    <div style="display:flex;gap:8px;margin-bottom:16px;flex-wrap:wrap">
        <button class="btn btn-sm" onclick="sendCommand('${m.agent_id}','restart')">重启 Agent</button>
        <button class="btn btn-sm" onclick="sendCommand('${m.agent_id}','shutdown')">关闭 Agent</button>
        <button class="btn btn-sm" onclick="downloadFile('${m.agent_id}', '/tmp/test.txt')">下载文件</button>
    </div>
    <div class="info-grid">
        <div class="info-card">
            <h4>系统信息</h4>
            <div class="info-row"><span class="label">发行版</span><span>${safeDistributorId}</span></div>
            <div class="info-row"><span class="label">描述</span><span>${safeDescription}</span></div>
            <div class="info-row"><span class="label">版本</span><span>${safeRelease}</span></div>
            <div class="info-row"><span class="label">代号</span><span>${safeCodename}</span></div>
        </div>
        <div class="info-card">
            <h4>网络信息</h4>
            <div class="info-row"><span class="label">公网 IP</span><span>${safePublicIp} <button class="copy-btn" onclick="navigator.clipboard.writeText('${safePublicIp}');Toast.success('已复制','IP')">复制</button></span></div>
            <div class="info-row"><span class="label">地理位置</span><span>${safeLocation}</span></div>
            <div class="info-row"><span class="label">状态</span><span style="color:${statusColor};font-weight:600">${statusText}</span></div>
        </div>
        <div class="info-card">
            <h4>硬件信息</h4>
            <div class="info-row"><span class="label">CPU</span><span>${safeCpuModel}</span></div>
            <div class="info-row"><span class="label">CPU 核心</span><span>${safeCpuCores}</span></div>
            <div class="info-row"><span class="label">内存</span><span>${safeMem}</span></div>
            <div class="info-row"><span class="label">磁盘</span><span>${safeDisk}</span></div>
        </div>
        <div class="info-card">
            <h4>运行信息</h4>
            <div class="info-row"><span class="label">运行时间</span><span>${formatUptime(m.uptime)}</span></div>
            <div class="info-row"><span class="label">Agent ID</span><span style="font-size:12px;font-family:'JetBrains Mono',monospace">${safeAgentId} <button class="copy-btn" onclick="navigator.clipboard.writeText('${m.agent_id}');Toast.success('已复制','Agent ID')">复制</button></span></div>
            <div class="info-row"><span class="label">最后上报</span><span>${lastSeenText}</span></div>
            <div class="info-row"><span class="label">标签</span><span><div class="info-tags">${tagsHtml || '<span style="color:var(--text-muted)">无</span>'}</div></span></div>
        </div>
    </div>`;
}

// ============ Terminal ============
function openTerminalFor(agentId) {
    document.getElementById('terminal-machine').value = agentId;
    switchTab('terminal');
    openTerminal();
}

function openTerminal() {
    const agentId = document.getElementById('terminal-machine').value;
    if (!agentId) { Toast.warning('请先选择一台机器'); return; }
    closeTerminal();
    const container = document.getElementById('terminal-container');
    container.innerHTML = '';

    term = new Terminal({
        cursorBlink: true, fontSize: 14,
        fontFamily: "'JetBrains Mono', 'Fira Code', 'Cascadia Code', monospace",
        theme: { background: '#0b1120', foreground: '#f1f5f9', cursor: '#3b82f6', selectionBackground: '#334155' },
    });

    fitAddon = new FitAddon.FitAddon();
    term.loadAddon(fitAddon);
    term.open(container);
    fitAddon.fit();

    const sessionId = 'sess-' + crypto.randomUUID().slice(0, 9);
    currentTerminal = `${agentId}:${sessionId}`;
    const hostname = machines[agentId]?.hostname || agentId;
    term.writeln(`\x1b[1;34m[Multi-Ops] 正在连接到 ${escapeHtml(hostname)}...\x1b[0m\r`);
    Toast.info(`终端连接中: ${hostname}`, 2000);

    term.onData((data) => {
        sendWS({ type: 'terminal_input', agent_id: agentId, data: { session_id: sessionId, data: data } });
    });

    term.onResize(({cols, rows}) => {
        sendWS({ type: 'terminal_resize', agent_id: agentId, data: { session_id: sessionId, cols, rows } });
    });

    new ResizeObserver(() => { if (fitAddon) fitAddon.fit(); }).observe(container);
    sendWS({ type: 'terminal_input', agent_id: agentId, data: { session_id: sessionId, data: '\n' } });
}

function closeTerminal() {
    if (term) { term.dispose(); term = null; }
    currentTerminal = null; fitAddon = null;
    Toast.info('终端已断开');
}

function updateTerminalSelect() {
    const select = document.getElementById('terminal-machine');
    const current = select.value;
    select.innerHTML = '<option value="">选择机器...</option>';
    Object.values(machines)
        .filter(m => m.status === 'online')
        .sort((a, b) => (a.hostname || '').localeCompare(b.hostname || ''))
        .forEach(m => {
            const opt = document.createElement('option');
            opt.value = m.agent_id;
            opt.textContent = `${m.hostname || m.agent_id} (${m.public_ip || 'N/A'})`;
            select.appendChild(opt);
        });
    if (current) select.value = current;
}

// ============ Batch Execution ============
function updateExecTargets() {
    updateCheckboxList('exec-targets');
}

function updateUploadTargets() {
    updateCheckboxList('upload-targets');
}

function updateCheckboxList(containerId) {
    const container = document.getElementById(containerId);
    const list = Object.values(machines)
        .sort((a, b) => { if (a.status !== b.status) return a.status === 'online' ? -1 : 1; return (a.hostname || '').localeCompare(b.hostname || ''); });

    container.innerHTML = '';
    for (const m of list) {
        const icon = m.status === 'online' ? '●' : '○';
        const safeName = escapeHtml(m.hostname || m.agent_id);
        const label = document.createElement('label');
        label.innerHTML = `<input type="checkbox" value="${m.agent_id}" ${m.status === 'online' ? '' : 'disabled'}> ${icon} ${safeName}`;
        container.appendChild(label);
    }
}

function selectAllMachines() { document.querySelectorAll('#exec-targets input[type=checkbox]').forEach(cb => cb.checked = true); }
function deselectAllMachines() { document.querySelectorAll('#exec-targets input[type=checkbox]').forEach(cb => cb.checked = false); }
function selectOnlineMachines() { document.querySelectorAll('#exec-targets input[type=checkbox]').forEach(cb => { cb.checked = !cb.disabled; }); }

function getSelectedAgentIds(containerId) {
    const ids = [];
    document.querySelectorAll(`#${containerId} input[type=checkbox]:checked`).forEach(cb => ids.push(cb.value));
    return ids;
}

const snippets = {
    sysinfo: 'echo "=== System Info ==="\nuname -a\ncat /etc/os-release | head -5\nuptime',
    disk: 'echo "=== Disk Usage ==="\ndf -h\necho "=== Inodes ==="\ndf -i',
    net: 'echo "=== Network ==="\nip addr show | grep "inet "\necho "=== Connections ==="\nss -tlnp | head -20',
    process: 'echo "=== Top CPU ==="\nps aux --sort=-%cpu | head -10\necho "=== Top Memory ==="\nps aux --sort=-%mem | head -10',
};

function insertSnippet(name) {
    document.getElementById('exec-script').value = snippets[name] || '';
    Toast.info('已插入脚本模板');
}

function executeScript() {
    const script = document.getElementById('exec-script').value;
    if (!script.trim()) { Toast.warning('请输入要执行的脚本'); return; }
    const targets = getSelectedAgentIds('exec-targets');
    if (targets.length === 0) { Toast.warning('请选择至少一台目标机器'); return; }
    const timeout = parseInt(document.getElementById('exec-timeout').value) || 60;
    const btn = document.getElementById('exec-btn');
    btn.disabled = true;
    btn.innerHTML = '<span class="spinner"></span> 执行中...';

    const container = document.getElementById('exec-output');
    container.innerHTML = '<div style="display:flex;align-items:center;gap:8px;color:var(--text-secondary)"><span class="spinner"></span> 正在分发到 ' + targets.length + ' 台机器...</div>';

    sendWS({
        type: 'exec_request',
        data: { script, timeout, agent_ids: targets }
    });

    // Reset button after 5s if no response
    setTimeout(() => {
        btn.disabled = false;
        btn.innerHTML = '批量执行 <span class="shortcut-hint">Ctrl+Enter</span>';
    }, 5000);
}

function addExecResult(agentId, data) {
    const container = document.getElementById('exec-output');
    // Clear loading state
    if (container.querySelector(':scope > div:not(.exec-result-item)')) container.innerHTML = '';

    const m = machines[agentId] || {};
    const hostname = escapeHtml(m.hostname || agentId);
    const exitClass = data.exit_code === 0 ? 'success' : 'fail';
    const exitText = data.exit_code === 0 ? '成功' : `失败 (${data.exit_code})`;

    const item = document.createElement('div');
    item.className = 'exec-result-item';
    item.innerHTML = `
        <div class="result-header">
            <span class="result-host">${hostname}</span>
            <span class="result-exit ${exitClass}">${exitText}</span>
        </div>
        <div class="result-output">${escapeHtml(data.output || '')}${data.error ? '\n[STDERR] ' + escapeHtml(data.error) : ''}</div>`;
    container.appendChild(item);
    container.scrollTop = container.scrollHeight;

    if (data.exit_code === 0) {
        Toast.success(`${m.hostname || agentId}: 执行成功`);
    } else {
        Toast.error(`${m.hostname || agentId}: 执行失败 (exit ${data.exit_code})`);
    }
}

// ============ File Upload ============
function uploadFile() {
    const path = document.getElementById('upload-path').value;
    const content = document.getElementById('upload-content').value;
    const mode = document.getElementById('upload-mode').value;
    const overwrite = document.getElementById('upload-overwrite').checked;
    const targets = getSelectedAgentIds('upload-targets');

    if (!path) { Toast.warning('请输入远程路径'); return; }
    if (!content) { Toast.warning('请输入文件内容'); return; }
    if (targets.length === 0) { Toast.warning('请选择目标机器'); return; }

    const btn = document.getElementById('upload-btn');
    btn.disabled = true;
    btn.textContent = '分发中...';

    document.getElementById('upload-result').innerHTML = '<div style="display:flex;align-items:center;gap:8px;color:var(--text-secondary)"><span class="spinner"></span> 正在分发到 ' + targets.length + ' 台机器...</div>';

    fetch('/api/file/upload', {
        method: 'POST',
        headers: authHeaders(),
        body: JSON.stringify({ path, content, mode, overwrite, agent_ids: targets })
    })
    .then(r => r.json())
    .then(data => {
        if (data.error) {
            document.getElementById('upload-result').innerHTML = `<span style="color:var(--red)">错误: ${escapeHtml(data.error)}</span>`;
            Toast.error('分发失败: ' + data.error);
        } else {
            document.getElementById('upload-result').innerHTML = `<span style="color:var(--green)">已分发到 ${data.count} 台机器</span> <span style="color:var(--text-muted);font-size:12px">(ID: ${data.request_id})</span>`;
            Toast.success(`已分发到 ${data.count} 台机器`);
        }
    })
    .catch(() => {
        document.getElementById('upload-result').innerHTML = '<span style="color:var(--red)">网络错误</span>';
        Toast.error('网络错误');
    })
    .finally(() => {
        btn.disabled = false;
        btn.textContent = '分发文件';
    });
}

function addUploadResult(agentId, data) {
    const container = document.getElementById('upload-result');
    const m = machines[agentId] || {};
    const hostname = escapeHtml(m.hostname || agentId);
    const status = data.success ? `<span style="color:var(--green)">成功</span>` : `<span style="color:var(--red)">失败: ${escapeHtml(data.error || '')}</span>`;
    container.innerHTML += `\n${hostname}: ${status}`;
}

// ============ History ============
function refreshHistory() {
    fetch('/api/exec/history?limit=20', {headers: authHeaders()})
    .then(r => r.json())
    .then(records => renderHistory(records))
    .catch(() => {});
}

function renderHistory(records) {
    const container = document.getElementById('history-list');
    if (!records || records.length === 0) {
        container.innerHTML = '<div class="empty-state"><div class="empty-state-icon">&#x1F4CB;</div>暂无执行历史</div>';
        return;
    }

    container.innerHTML = records.map(r => {
        const time = new Date(r.started_at).toLocaleString('zh-CN');
        const statusClass = r.status || 'completed';
        const targets = (r.agent_ids || []).length;
        const safeUser = escapeHtml(r.user || '');
        return `
        <div class="history-item" onclick="toggleHistoryDetail(this, '${r.id}')">
            <div class="history-header">
                <span class="history-script">${escapeHtml(r.script || '').substring(0, 80)}</span>
                <span class="history-status ${statusClass}">${r.status === 'running' ? '运行中' : '已完成'} · ${targets}台 · ${safeUser}</span>
            </div>
            <div class="history-time">${time}</div>
            <div class="history-detail" style="display:none" id="hd-${r.id}"></div>
        </div>`;
    }).join('');
}

function toggleHistoryDetail(el, id) {
    const detail = document.getElementById(`hd-${id}`);
    if (detail.style.display === 'none') {
        fetch(`/api/exec/detail?id=${id}`, {headers: authHeaders()})
        .then(r => r.json())
        .then(data => {
            let html = `脚本:\n${data.script}\n\n`;
            for (const [aid, result] of Object.entries(data.results || {})) {
                const m = machines[aid] || {};
                html += `--- ${m.hostname || aid} (exit: ${result.exit_code}) ---\n${result.output || result.error || '无输出'}\n\n`;
            }
            detail.textContent = html;
            detail.style.display = 'block';
        });
    } else {
        detail.style.display = 'none';
    }
}

// ============ Tabs ============
function switchTab(name) {
    document.querySelectorAll('.tab').forEach(t => t.classList.remove('active'));
    document.querySelectorAll('.tab-content').forEach(t => t.classList.remove('active'));

    const tabMap = {terminal: 0, exec: 1, upload: 2, history: 3, info: 4};
    const idx = tabMap[name];
    if (idx !== undefined) {
        document.querySelectorAll('.tab')[idx]?.classList.add('active');
    }
    document.getElementById(`tab-${name}`).classList.add('active');
    if (name === 'terminal' && fitAddon) setTimeout(() => fitAddon.fit(), 100);
    if (name === 'history') refreshHistory();
}

// ============ Utility ============
function sendWS(msg) {
    if (window._dashboardWS && window._dashboardWS.readyState === WebSocket.OPEN) {
        window._dashboardWS.send(JSON.stringify(msg));
    } else {
        Toast.warning('WebSocket 未连接，消息未发送');
    }
}

function refreshMachines() {
    fetch('/api/machines', {headers: authHeaders()})
    .then(r => {
        if (r.status === 401) { handleLogout(); throw new Error(); }
        return r.json();
    })
    .then(list => {
        machines = {};
        (list || []).forEach(m => { machines[m.agent_id] = m; });
        renderMachineList();
        updateStats();
        Toast.info('机器列表已刷新', 2000);
    })
    .catch(() => {});
}

function escapeHtml(str) {
    const div = document.createElement('div');
    div.textContent = str;
    return div.innerHTML;
}

// ============ Real-time Metrics ============
function updateMachineMetrics(agentId) {
    const m = machines[agentId];
    if (!m || !m.metrics) return;
    const infoPanel = document.getElementById('machine-info');
    if (infoPanel.querySelector('.metrics-live')) {
        const el = infoPanel.querySelector('.metrics-live');
        el.innerHTML = renderLiveMetrics(m.metrics);
    }
}

function renderLiveMetrics(mt) {
    const cpuPct = mt.cpu_percent?.toFixed(1) || 0;
    const memPct = mt.mem_percent?.toFixed(1) || 0;
    const diskPct = mt.disk_percent?.toFixed(1) || 0;
    return `
    <div style="display:grid;grid-template-columns:repeat(3,1fr);gap:12px;margin-top:8px">
        <div class="gauge"><div class="gauge-label">CPU</div><div class="gauge-value" style="color:${cpuPct>80?'var(--red)':cpuPct>50?'var(--yellow)':'var(--green)'}">${cpuPct}%</div></div>
        <div class="gauge"><div class="gauge-label">内存</div><div class="gauge-value" style="color:${memPct>80?'var(--red)':memPct>50?'var(--yellow)':'var(--green)'}">${memPct}%</div></div>
        <div class="gauge"><div class="gauge-label">磁盘</div><div class="gauge-value" style="color:${diskPct>80?'var(--red)':diskPct>50?'var(--yellow)':'var(--green)'}">${diskPct}%</div></div>
    </div>
    <div style="display:grid;grid-template-columns:repeat(4,1fr);gap:8px;margin-top:8px;font-size:13px">
        <div><span class="label">Load1:</span> ${mt.load1?.toFixed(2) || '-'}</div>
        <div><span class="label">Load5:</span> ${mt.load5?.toFixed(2) || '-'}</div>
        <div><span class="label">Load15:</span> ${mt.load15?.toFixed(2) || '-'}</div>
        <div><span class="label">TCP:</span> ${mt.tcp_conns || '-'}</div>
        <div><span class="label">进程:</span> ${mt.process_count || '-'}</div>
        <div><span class="label">RX:</span> ${formatBytes(mt.net_rx_bytes)}/s</div>
        <div><span class="label">TX:</span> ${formatBytes(mt.net_tx_bytes)}/s</div>
        <div><span class="label">内存:</span> ${mt.mem_used_mb || '-'}MB</div>
    </div>`;
}

function formatBytes(bytes) {
    if (!bytes) return '0B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return (bytes / Math.pow(k, i)).toFixed(1) + sizes[i];
}

// ============ Script Templates ============
function loadScriptTemplates() {
    fetch('/api/script-templates', {headers: authHeaders()})
    .then(r => r.json())
    .then(templates => {
        const container = document.querySelector('.exec-snippets');
        if (!container) return;
        container.innerHTML = '';
        templates.forEach(t => {
            const btn = document.createElement('button');
            btn.className = 'btn btn-sm';
            btn.textContent = t.name;
            btn.onclick = () => {
                document.getElementById('exec-script').value = t.script;
                Toast.info(`已加载模板: ${t.name}`);
            };
            container.appendChild(btn);
        });
    });
}

// ============ File Download ============
function downloadFile(agentId, path) {
    fetch(`/api/file/download?agent_id=${agentId}&path=${encodeURIComponent(path)}`, {headers: authHeaders()})
    .then(r => r.json())
    .then(data => {
        if (data.request_id) {
            document.getElementById('upload-result').innerHTML += `\n下载请求已发送: ${path}`;
            Toast.info(`正在下载: ${path}`);
        }
    })
    .catch(() => Toast.error('下载请求失败'));
}

function handleFileDownloadResp(data) {
    if (data && data.success) {
        const blob = new Blob([data.content], {type: 'text/plain'});
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = data.path.split('/').pop();
        a.click();
        URL.revokeObjectURL(url);
        Toast.success(`文件已下载: ${data.path}`);
    } else if (data) {
        Toast.error('文件下载失败: ' + (data.error || '未知错误'));
    }
}

// ============ Remote Command ============
function sendCommand(agentId, command) {
    const hostname = machines[agentId]?.hostname || agentId;
    if (!confirm(`确定要对 ${hostname} 执行 "${command}" 操作?`)) return;
    fetch('/api/command', {
        method: 'POST',
        headers: authHeaders(),
        body: JSON.stringify({agent_id: agentId, command: command})
    })
    .then(r => r.json())
    .then(data => {
        if (data.status === 'sent') Toast.success(`命令 "${command}" 已发送到 ${hostname}`);
    })
    .catch(() => Toast.error('命令发送失败'));
}

// ============ Keyboard Shortcuts ============
document.addEventListener('keydown', (e) => {
    // Don't trigger shortcuts when typing in inputs/textareas
    const tag = e.target.tagName;
    const isInput = tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT';

    // Escape: close modal / logout (if on login page)
    if (e.key === 'Escape') {
        const modal = document.getElementById('totp-modal');
        if (modal.style.display !== 'none') {
            closeTOTPModal();
            e.preventDefault();
            return;
        }
    }

    if (isInput) return;

    // Tab switching: 1-5
    const tabKeys = {'1': 'terminal', '2': 'exec', '3': 'upload', '4': 'history', '5': 'info'};
    if (tabKeys[e.key] && !e.ctrlKey && !e.metaKey) {
        switchTab(tabKeys[e.key]);
        e.preventDefault();
        return;
    }

    // Ctrl+Enter: execute script
    if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
        if (document.getElementById('tab-exec').classList.contains('active')) {
            executeScript();
            e.preventDefault();
        }
    }

    // R: refresh machines
    if (e.key === 'r' && !e.ctrlKey && !e.metaKey) {
        refreshMachines();
        e.preventDefault();
    }
});

// ============ Init ============
document.addEventListener('DOMContentLoaded', () => {
    Toast.init();

    if (token) {
        fetch('/api/machines', {headers: authHeaders()})
        .then(r => {
            if (r.status === 401) { handleLogout(); return; }
            showApp();
        }).catch(() => showLogin());
    } else {
        showLogin();
    }

    document.getElementById('machine-filter').addEventListener('input', renderMachineList);

    // Focus login form
    const userInput = document.getElementById('login-user');
    if (userInput) userInput.focus();
});
