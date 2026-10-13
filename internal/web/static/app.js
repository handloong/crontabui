(function () {
  'use strict';

  var state = {
    jobs: [],
    editingId: null,
    logJobId: null,
    logTab: 'job',
    timer: null,
    toastTimer: null,
    updateEnabled: false,
    updateRunning: false,
    updatePolling: null
  };

  var el = {
    logDir: document.getElementById('meta-logdir'),
    logout: document.getElementById('btn-logout'),
    refresh: document.getElementById('btn-refresh'),
    add: document.getElementById('btn-new'),
    statTotal: document.getElementById('stat-total'),
    statEnabled: document.getElementById('stat-enabled'),
    statDisabled: document.getElementById('stat-disabled'),
    statForeign: document.getElementById('stat-foreign'),
    rows: document.getElementById('job-rows'),
    empty: document.getElementById('jobs-empty'),
    foreign: document.getElementById('foreign-lines'),

    jobDialog: document.getElementById('job-dialog'),
    jobForm: document.getElementById('job-form'),
    jobTitle: document.getElementById('job-dialog-title'),
    jobClose: document.getElementById('btn-job-close'),
    jobCancel: document.getElementById('btn-job-cancel'),
    jobSave: document.getElementById('btn-job-save'),
    name: document.getElementById('job-name'),
    preset: document.getElementById('job-preset'),
    schedule: document.getElementById('job-schedule'),
    scheduleNote: document.getElementById('schedule-note'),
    command: document.getElementById('job-command'),
    commandNote: document.getElementById('command-note'),
    capture: document.getElementById('job-capture'),
    enabled: document.getElementById('job-enabled'),

    logDialog: document.getElementById('log-dialog'),
    logTitle: document.getElementById('log-dialog-title'),
    logClose: document.getElementById('btn-log-close'),
    logReload: document.getElementById('btn-log-reload'),
    logExport: document.getElementById('btn-log-export'),
    logClear: document.getElementById('btn-log-clear'),
    logTabs: document.getElementById('log-tabs'),
    logLines: document.getElementById('log-lines'),
    logAuto: document.getElementById('log-auto'),
    logMeta: document.getElementById('log-meta'),
    logView: document.getElementById('log-view'),

    version: document.getElementById('meta-version'),
    updateBtn: document.getElementById('btn-update'),
    updateDialog: document.getElementById('update-dialog'),
    updateClose: document.getElementById('btn-update-close'),
    updateCancel: document.getElementById('btn-update-cancel'),
    updateApply: document.getElementById('btn-update-apply'),
    updateSpinner: document.getElementById('update-spinner'),
    updateMessage: document.getElementById('update-message'),
    updateVers: document.getElementById('update-vers'),
    updateCurrent: document.getElementById('update-current'),
    updateLatest: document.getElementById('update-latest'),
    updateBarTrack: document.getElementById('update-bar-track'),
    updateBar: document.getElementById('update-bar'),
    updateInfo: document.getElementById('update-info'),
    updateNotes: document.getElementById('update-notes'),

    toast: document.getElementById('toast')
  };

  function escapeHtml(value) {
    return String(value === null || value === undefined ? '' : value)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;');
  }

  function formatSize(bytes) {
    if (!bytes) { return '0 B'; }
    var units = ['B', 'KB', 'MB', 'GB'];
    var i = 0;
    var n = bytes;
    while (n >= 1024 && i < units.length - 1) { n /= 1024; i += 1; }
    return (i === 0 ? n : n.toFixed(1)) + ' ' + units[i];
  }

  function toast(message, kind) {
    el.toast.textContent = message;
    el.toast.className = 'toast' + (kind ? ' is-' + kind : '');
    el.toast.hidden = false;
    if (state.toastTimer) { clearTimeout(state.toastTimer); }
    state.toastTimer = setTimeout(function () { el.toast.hidden = true; }, 2800);
  }

  function api(method, path, body) {
    var options = { method: method, credentials: 'same-origin' };
    if (body !== undefined && body !== null) {
      options.headers = { 'Content-Type': 'application/json' };
      options.body = JSON.stringify(body);
    }
    return fetch(path, options).then(function (res) {
      if (res.status === 401) {
        window.location.replace('/login');
        throw new Error('会话已过期');
      }
      return res.json().catch(function () { return null; }).then(function (payload) {
        if (!res.ok || !payload || payload.ok !== true) {
          throw new Error((payload && payload.error) || ('请求失败（HTTP ' + res.status + '）'));
        }
        return payload.data;
      });
    });
  }

  /* ---------- 渲染 ---------- */

  function render(data) {
    state.jobs = data.jobs || [];
    el.logDir.textContent = '日志目录：' + (data.logDir || '-');
    el.statTotal.textContent = state.jobs.length;
    el.statEnabled.textContent = data.enabledCount || 0;
    el.statDisabled.textContent = data.disabledCount || 0;

    var foreign = data.foreign || [];
    el.statForeign.textContent = foreign.length;
    if (foreign.length === 0) {
      el.foreign.textContent = '（无）';
    } else {
      var blocks = foreign.map(function (block) {
        return (block.lines || []).join('\n');
      });
      el.foreign.textContent = blocks.join('\n\n');
    }

    if (state.jobs.length === 0) {
      el.rows.innerHTML = '';
      el.empty.hidden = false;
      return;
    }
    el.empty.hidden = true;

    el.rows.innerHTML = state.jobs.map(function (job) {
      var status = job.enabled
        ? '<span class="badge badge-on">已启用</span>'
        : '<span class="badge badge-off">已停用</span>';
      var capture = job.captureLog
        ? '<span class="badge badge-info">捕获输出</span>'
        : '<span class="badge badge-off">不捕获</span>';
      var warning = job.warning
        ? '<div><span class="badge badge-warn" title="' + escapeHtml(job.warning) + '">命令含 %</span></div>'
        : '';
      var logHint = job.logExists
        ? '<span class="badge badge-info">' + formatSize(job.logSize) + '</span>'
        : '<span class="badge badge-off">无日志</span>';

      // data-label 供窄屏下的卡片式布局用（CSS 里通过 ::before 显示字段名）
      return '' +
        '<tr data-id="' + escapeHtml(job.id) + '">' +
        '<td data-label="状态">' + status + '</td>' +
        '<td data-label="任务">' +
        '<span class="cell-name">' + (job.name ? escapeHtml(job.name) : '<span class="muted">未命名任务</span>') + '</span>' +
        '<span class="id-tag">#' + escapeHtml(job.id) + '</span>' +
        '</td>' +
        '<td data-label="执行周期">' +
        '<span class="cron">' + escapeHtml(job.schedule) + '</span>' +
        '<span class="sched-desc">' + escapeHtml(job.description) + '</span>' +
        '</td>' +
        '<td data-label="命令">' +
        '<div class="cmd" title="' + escapeHtml(job.command) + '">' + escapeHtml(job.command) + '</div>' +
        '<div>' + capture + ' ' + logHint + '</div>' +
        warning +
        '</td>' +
        '<td data-label="操作">' +
        '<div class="row-actions">' +
        '<button class="btn btn-ghost btn-mini" data-act="toggle" data-id="' + escapeHtml(job.id) + '">' +
        (job.enabled ? '停用' : '启用') + '</button>' +
        '<button class="btn btn-ghost btn-mini" data-act="log" data-id="' + escapeHtml(job.id) + '">日志</button>' +
        '<button class="btn btn-ghost btn-mini" data-act="run" data-id="' + escapeHtml(job.id) + '">立即执行</button>' +
        '<button class="btn btn-ghost btn-mini" data-act="edit" data-id="' + escapeHtml(job.id) + '">编辑</button>' +
        '<button class="btn btn-danger btn-mini" data-act="delete" data-id="' + escapeHtml(job.id) + '">删除</button>' +
        '</div>' +
        '</td>' +
        '</tr>';
    }).join('');
  }

  function load() {
    return api('GET', '/api/jobs')
      .then(render)
      .catch(function (err) { toast(err.message, 'error'); });
  }

  function findJob(id) {
    for (var i = 0; i < state.jobs.length; i += 1) {
      if (state.jobs[i].id === id) { return state.jobs[i]; }
    }
    return null;
  }

  /* ---------- 编辑器 ---------- */

  function openEditor(job) {
    state.editingId = job ? job.id : null;
    el.jobTitle.textContent = job ? '编辑任务' : '新增任务';
    el.name.value = job ? job.name : '';
    el.schedule.value = job ? job.schedule : '*/5 * * * *';
    el.command.value = job ? job.command : '';
    el.capture.checked = job ? job.captureLog : true;
    el.enabled.checked = job ? job.enabled : true;
    el.preset.value = '';
    setPresetIfKnown();
    validateSchedule();
    el.jobDialog.showModal();
    el.name.focus();
  }

  function setPresetIfKnown() {
    var value = el.schedule.value.trim();
    var options = el.preset.options;
    el.preset.value = '';
    for (var i = 0; i < options.length; i += 1) {
      if (options[i].value && options[i].value === value) {
        el.preset.value = value;
        return;
      }
    }
  }

  function setNote(node, text, kind) {
    node.textContent = text;
    node.className = 'field-note' + (kind ? ' ' + kind : '');
  }

  function validateSchedule() {
    var schedule = el.schedule.value.trim();
    if (!schedule) {
      setNote(el.scheduleNote, '请填写执行周期表达式。', 'bad');
      return;
    }
    var path = '/api/validate?schedule=' + encodeURIComponent(schedule) +
      '&command=' + encodeURIComponent(el.command.value);
    api('GET', path).then(function (data) {
      if (data.valid) {
        setNote(el.scheduleNote, '解析结果：' + data.description + '（' + schedule + '）', 'ok');
      } else {
        setNote(el.scheduleNote, data.error, 'bad');
      }
      if (data.warning) {
        setNote(el.commandNote, data.warning, 'warn');
      } else {
        setNote(el.commandNote, '以当前用户身份执行，与在 crontab 中手写完全一致。');
      }
    }).catch(function (err) {
      setNote(el.scheduleNote, err.message, 'bad');
    });
  }

  function submitEditor(event) {
    event.preventDefault();
    var payload = {
      name: el.name.value.trim(),
      schedule: el.schedule.value.trim(),
      command: el.command.value.trim(),
      captureLog: el.capture.checked,
      enabled: el.enabled.checked
    };
    if (!payload.command) {
      setNote(el.commandNote, '执行命令不能为空。', 'bad');
      el.command.focus();
      return;
    }

    el.jobSave.disabled = true;
    el.jobSave.textContent = '保存中…';
    var request = state.editingId
      ? api('PUT', '/api/jobs/' + encodeURIComponent(state.editingId), payload)
      : api('POST', '/api/jobs', payload);

    request.then(function () {
      el.jobDialog.close();
      toast(state.editingId ? '任务已更新' : '任务已创建', 'ok');
      return load();
    }).catch(function (err) {
      setNote(el.scheduleNote, err.message, 'bad');
      toast(err.message, 'error');
    }).then(function () {
      el.jobSave.disabled = false;
      el.jobSave.textContent = '保存';
    });
  }

  /* ---------- 日志 ---------- */

  function openLog(jobId) {
    state.logJobId = jobId;
    state.logTab = 'job';
    setActiveTab();
    el.logTitle.textContent = jobId ? '任务日志 · #' + jobId : '日志';
    el.logDialog.showModal();
    loadLog();
  }

  function setActiveTab() {
    var tabs = el.logTabs.querySelectorAll('.tab');
    for (var i = 0; i < tabs.length; i += 1) {
      var active = tabs[i].getAttribute('data-tab') === state.logTab;
      tabs[i].className = active ? 'tab is-active' : 'tab';
    }
    // 导出/删除只对「任务日志」有意义：系统 cron 日志由 syslog/journald 维护，
    // 本程序只读——既不该在界面里删它，导出也算不上「导出本程序的产物」。
    var isJobLog = state.logTab === 'job' && !!state.logJobId;
    el.logExport.hidden = !isJobLog;
    el.logClear.hidden = !isJobLog;
    // 切页时先放开，再由 loadLog 依据文件是否存在决定是否禁用
    el.logExport.disabled = false;
    el.logClear.disabled = false;
  }

  function loadLog() {
    var lines = el.logLines.value;
    var path;
    if (state.logTab === 'system') {
      path = '/api/logs/system?lines=' + encodeURIComponent(lines);
    } else if (state.logJobId) {
      path = '/api/jobs/' + encodeURIComponent(state.logJobId) + '/log?lines=' + encodeURIComponent(lines);
    } else {
      return;
    }
    api('GET', path).then(function (data) {
      var meta = [];
      if (state.logTab === 'system') {
        meta.push('来源：' + (data.source || '未找到'));
      } else if (data.file) {
        meta.push('文件：' + data.file);
      }
      meta.push('显示末尾 ' + data.lines + ' 行');
      el.logMeta.textContent = meta.join('　|　');

      if (data.message) {
        el.logView.textContent = data.message;
        return;
      }
      if (state.logTab === 'job') {
        // 文件还不存在时禁用导出/删除，避免点了才报错
        var missing = data.exists === false;
        el.logExport.disabled = missing;
        el.logClear.disabled = missing;
      }
      var content = data.content || '';
      if (!content) {
        el.logView.textContent = state.logTab === 'system'
          ? '（暂无 cron 日志输出）'
          : '（日志为空，可能是任务尚未执行，或未开启「捕获输出」）';
        return;
      }
      el.logView.textContent = content;
      el.logView.scrollTop = el.logView.scrollHeight;
    }).catch(function (err) {
      el.logView.textContent = err.message;
    });
  }

  // 导出走 fetch + Blob，而不是直接跳转下载地址：
  // 这样失败时能拿到 JSON 里的中文错误提示，页面也不会被导航走。
  function exportLog() {
    if (!state.logJobId) { return; }
    var url = '/api/jobs/' + encodeURIComponent(state.logJobId) + '/log/download';
    el.logExport.disabled = true;
    fetch(url, { credentials: 'same-origin' })
      .then(function (res) {
        if (res.status === 401) {
          window.location.replace('/login');
          throw new Error('会话已过期');
        }
        if (!res.ok) {
          return res.json().catch(function () { return null; }).then(function (payload) {
            throw new Error((payload && payload.error) || ('导出失败（HTTP ' + res.status + '）'));
          });
        }
        return res.blob().then(function (blob) {
          return { blob: blob, name: res.headers.get('X-Log-Filename') || '' };
        });
      })
      .then(function (got) {
        var name = safeDecode(got.name) || ('log-' + state.logJobId + '.log');
        var href = URL.createObjectURL(got.blob);
        var a = document.createElement('a');
        a.href = href;
        a.download = name;
        document.body.appendChild(a);
        a.click();
        document.body.removeChild(a);
        // 下载是异步启动的，立刻 revoke 可能让下载拿不到数据
        setTimeout(function () { URL.revokeObjectURL(href); }, 10000);
        toast('已导出 ' + name, 'ok');
      })
      .catch(function (err) { toast(err.message, 'error'); })
      .then(function () { el.logExport.disabled = false; });
  }

  function clearLog() {
    if (!state.logJobId) { return; }
    var job = findJob(state.logJobId);
    var label = (job && job.name) || ('#' + state.logJobId);
    if (!window.confirm('确定清空任务「' + label + '」的日志内容吗？清空后不可恢复。')) { return; }
    el.logClear.disabled = true;
    api('DELETE', '/api/jobs/' + encodeURIComponent(state.logJobId) + '/log')
      .then(function (data) {
        toast('已清空日志（释放 ' + formatSize(data.freed || 0) + '）', 'ok');
        return loadLog();
      })
      .catch(function (err) { toast(err.message, 'error'); })
      .then(function () { el.logClear.disabled = false; });
  }

  function safeDecode(s) {
    if (!s) { return ''; }
    try {
      return decodeURIComponent(s);
    } catch (e) {
      return '';
    }
  }

  function setAutoRefresh(on) {
    if (state.timer) {
      clearInterval(state.timer);
      state.timer = null;
    }
    if (on) {
      state.timer = setInterval(function () {
        if (!el.logDialog.open) { return; }
        loadLog();
      }, 5000);
    }
  }

  /* ---------- 自助更新 ---------- */

  function setUpdateMessage(text, kind) {
    el.updateMessage.textContent = text;
    el.updateMessage.className = 'update-msg' + (kind ? ' is-' + kind : '');
    el.updateSpinner.hidden = kind !== 'busy';
  }

  // progress < 0 表示进度未知，渲染成滑动的不确定进度条
  function setUpdateBar(progress) {
    el.updateBarTrack.hidden = false;
    if (typeof progress === 'number' && progress >= 0) {
      el.updateBar.classList.remove('is-indeterminate');
      el.updateBar.style.width = Math.round(progress * 100) + '%';
      return;
    }
    el.updateBar.style.width = '';
    el.updateBar.classList.add('is-indeterminate');
  }

  function resetUpdateDialog() {
    el.updateVers.hidden = true;
    el.updateInfo.hidden = true;
    el.updateNotes.hidden = true;
    el.updateBarTrack.hidden = true;
    el.updateBar.style.width = '0%';
    el.updateBar.classList.remove('is-indeterminate');
    el.updateMessage.className = 'update-msg';
    el.updateApply.disabled = true;
    el.updateApply.textContent = '立即更新';
    el.updateCancel.disabled = false;
    el.updateCurrent.textContent = '—';
    el.updateLatest.textContent = '—';
  }

  function stopUpdatePolling() {
    if (state.updatePolling) {
      clearTimeout(state.updatePolling);
      state.updatePolling = null;
    }
  }

  // 启动时问一次接口，决定顶栏要不要显示更新入口，
  // 顺便用服务端返回的版本号覆盖 {{VERSION}} 的静态值。
  function initUpdate() {
    api('GET', '/api/update').then(function (data) {
      state.updateEnabled = !!data.enabled;
      el.updateBtn.hidden = !state.updateEnabled;
      if (data.current) { el.version.textContent = data.current; }
    }).catch(function () {
      // 更新接口不可用不应影响主流程，静默跳过
    });
  }

  function openUpdateDialog() {
    resetUpdateDialog();
    state.updateRunning = false;
    el.updateDialog.showModal();
    setUpdateMessage('正在检查最新版本…', 'busy');
    api('POST', '/api/update/check').then(renderChecked).catch(function (err) {
      setUpdateMessage(err.message, 'error');
    });
  }

  function renderChecked(data) {
    if (data.current) { el.version.textContent = data.current; }
    el.updateCurrent.textContent = data.current || 'dev';
    el.updateLatest.textContent = data.latest || '—';
    el.updateVers.hidden = false;

    var info = [];
    if (data.platform) { info.push('平台 ' + data.platform); }
    if (data.asset) {
      info.push('产物 ' + data.asset + (data.assetSize ? '（' + formatSize(data.assetSize) + '）' : ''));
    }
    if (data.target) { info.push('安装位置 ' + data.target); }
    el.updateInfo.textContent = info.join('　|　');
    el.updateInfo.hidden = info.length === 0;

    if (data.hasUpdate) {
      el.updateNotes.hidden = !data.notes;
      el.updateNotes.textContent = data.notes || '';
      setUpdateMessage('发现新版本 ' + data.latest + '，可以立即更新。', 'ok');
      el.updateApply.disabled = false;
      return;
    }
    if (data.latest && data.asset) {
      setUpdateMessage('已是最新版本。', 'ok');
      return;
    }
    setUpdateMessage(data.message || '没有可用的更新。', 'warn');
  }

  function applyUpdate() {
    if (!window.confirm('将下载新版本并替换当前程序，随后服务会自动重启。\n重启期间页面会短暂断开，确定继续吗？')) {
      return;
    }
    state.updateRunning = true;
    el.updateApply.disabled = true;
    el.updateApply.textContent = '更新中…';
    el.updateCancel.disabled = true;
    setUpdateBar(-1);
    setUpdateMessage('正在开始更新…', 'busy');

    api('POST', '/api/update/apply').then(function () {
      stopUpdatePolling();
      state.updatePolling = setTimeout(pollUpdate, 600);
    }).catch(function (err) {
      failUpdate(err.message);
    });
  }

  function failUpdate(message) {
    state.updateRunning = false;
    el.updateBarTrack.hidden = true;
    el.updateApply.disabled = false;
    el.updateApply.textContent = '重试更新';
    el.updateCancel.disabled = false;
    setUpdateMessage(message, 'error');
  }

  function pollUpdate() {
    state.updatePolling = null;
    api('GET', '/api/update').then(function (data) {
      renderProgress(data);
      if (data.phase === 'restarting' || data.phase === 'failed') { return; }
      state.updatePolling = setTimeout(pollUpdate, 800);
    }).catch(function () {
      // 服务已开始重启，请求会失败，转入健康检查等待
      waitForRestart();
    });
  }

  function renderProgress(data) {
    if (data.phase === 'failed') {
      failUpdate(data.error || '更新失败。');
      return;
    }
    if (data.phase === 'restarting') {
      waitForRestart();
      return;
    }
    if (data.phase === 'idle' || data.phase === 'checking') {
      return;
    }
    setUpdateBar(data.phase === 'downloading' ? data.progress : -1);
    setUpdateMessage(data.message || '正在处理…', 'busy');
  }

  // 重启期间 HTTP 会中断，改为探测健康检查接口，服务回来后再刷新页面。
  function waitForRestart() {
    stopUpdatePolling();
    setUpdateBar(-1);
    setUpdateMessage('更新完成，服务正在重启…', 'busy');

    var tries = 0;
    var probe = function () {
      tries += 1;
      fetch('/healthz', { cache: 'no-store' }).then(function (res) {
        if (!res.ok) { throw new Error('尚未就绪'); }
        window.location.reload();
      }).catch(function () {
        if (tries >= 90) {
          setUpdateMessage('服务重启超时，请手动刷新页面确认状态。', 'error');
          return;
        }
        setTimeout(probe, 1000);
      });
    };
    // 先等一会儿再探测，给 systemd 留出重启的时间
    setTimeout(probe, 1500);
  }

  /* ---------- 事件绑定 ---------- */

  el.updateBtn.addEventListener('click', openUpdateDialog);
  el.updateClose.addEventListener('click', function () { el.updateDialog.close(); });
  el.updateCancel.addEventListener('click', function () { el.updateDialog.close(); });
  el.updateApply.addEventListener('click', applyUpdate);
  // 更新进行中禁止用 Esc 关闭，避免让人误以为更新被取消
  el.updateDialog.addEventListener('cancel', function (event) {
    if (state.updateRunning) { event.preventDefault(); }
  });
  el.updateDialog.addEventListener('close', stopUpdatePolling);

  el.refresh.addEventListener('click', function () {
    load().then(function () { toast('已刷新'); });
  });

  el.add.addEventListener('click', function () { openEditor(null); });
  el.jobClose.addEventListener('click', function () { el.jobDialog.close(); });
  el.jobCancel.addEventListener('click', function () { el.jobDialog.close(); });
  el.jobForm.addEventListener('submit', submitEditor);
  el.preset.addEventListener('change', function () {
    if (el.preset.value) {
      el.schedule.value = el.preset.value;
      validateSchedule();
    }
  });
  el.schedule.addEventListener('input', function () {
    setPresetIfKnown();
    validateSchedule();
  });
  el.command.addEventListener('input', validateSchedule);

  el.logout.addEventListener('click', function () {
    api('POST', '/api/logout').then(function () {
      window.location.replace('/login');
    }).catch(function () {
      window.location.replace('/login');
    });
  });

  el.rows.addEventListener('click', function (event) {
    var button = event.target.closest('button[data-act]');
    if (!button) { return; }
    var id = button.getAttribute('data-id');
    var action = button.getAttribute('data-act');
    var job = findJob(id);
    if (!job) { return; }

    if (action === 'edit') {
      openEditor(job);
      return;
    }
    if (action === 'log') {
      openLog(id);
      return;
    }
    if (action === 'toggle') {
      button.disabled = true;
      api('POST', '/api/jobs/' + encodeURIComponent(id) + '/toggle', { enabled: !job.enabled })
        .then(function () {
          toast(job.enabled ? '任务已停用' : '任务已启用', 'ok');
          return load();
        })
        .catch(function (err) { toast(err.message, 'error'); })
        .then(function () { button.disabled = false; });
      return;
    }
    if (action === 'run') {
      button.disabled = true;
      api('POST', '/api/jobs/' + encodeURIComponent(id) + '/run')
        .then(function () { toast('已在后台启动该任务', 'ok'); })
        .catch(function (err) { toast(err.message, 'error'); })
        .then(function () { button.disabled = false; });
      return;
    }
    if (action === 'delete') {
      var label = job.name || ('#' + id);
      if (!window.confirm('确定删除任务「' + label + '」吗？该操作会立即写入系统 crontab。')) { return; }
      button.disabled = true;
      api('DELETE', '/api/jobs/' + encodeURIComponent(id))
        .then(function () {
          toast('任务已删除', 'ok');
          return load();
        })
        .catch(function (err) { toast(err.message, 'error'); })
        .then(function () { button.disabled = false; });
    }
  });

  el.logClose.addEventListener('click', function () {
    el.logDialog.close();
  });
  el.logDialog.addEventListener('close', function () {
    el.logAuto.checked = false;
    setAutoRefresh(false);
  });
  el.logReload.addEventListener('click', loadLog);
  el.logExport.addEventListener('click', exportLog);
  el.logClear.addEventListener('click', clearLog);
  el.logLines.addEventListener('change', loadLog);
  el.logAuto.addEventListener('change', function () {
    setAutoRefresh(el.logAuto.checked);
  });
  el.logTabs.addEventListener('click', function (event) {
    var tab = event.target.closest('.tab');
    if (!tab) { return; }
    state.logTab = tab.getAttribute('data-tab');
    setActiveTab();
    loadLog();
  });

  initUpdate();
  load();
})();
