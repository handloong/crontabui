(function () {
  'use strict';

  var state = {
    jobs: [],
    editingId: null,
    logJobId: null,
    logTab: 'job',
    timer: null,
    toastTimer: null
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
    logTabs: document.getElementById('log-tabs'),
    logLines: document.getElementById('log-lines'),
    logAuto: document.getElementById('log-auto'),
    logMeta: document.getElementById('log-meta'),
    logView: document.getElementById('log-view'),

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

      return '' +
        '<tr data-id="' + escapeHtml(job.id) + '">' +
        '<td>' + status + '</td>' +
        '<td>' +
        '<span class="cell-name">' + (job.name ? escapeHtml(job.name) : '<span class="muted">未命名任务</span>') + '</span>' +
        '<span class="id-tag">#' + escapeHtml(job.id) + '</span>' +
        '</td>' +
        '<td>' +
        '<span class="cron">' + escapeHtml(job.schedule) + '</span>' +
        '<span class="sched-desc">' + escapeHtml(job.description) + '</span>' +
        '</td>' +
        '<td>' +
        '<div class="cmd" title="' + escapeHtml(job.command) + '">' + escapeHtml(job.command) + '</div>' +
        '<div>' + capture + ' ' + logHint + '</div>' +
        warning +
        '</td>' +
        '<td>' +
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

  /* ---------- 事件绑定 ---------- */

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

  load();
})();
