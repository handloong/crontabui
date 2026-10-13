(function () {
  'use strict';

  var form = document.getElementById('login-form');
  var input = document.getElementById('token');
  var toggle = document.getElementById('btn-toggle');
  var submit = document.getElementById('btn-submit');
  var errorBox = document.getElementById('error-box');
  var errorText = document.getElementById('error-text');

  function showError(message) {
    errorText.textContent = message;
    errorBox.hidden = false;
  }

  function clearError() {
    errorText.textContent = '';
    errorBox.hidden = true;
  }

  toggle.addEventListener('click', function () {
    var reveal = input.type === 'password';
    input.type = reveal ? 'text' : 'password';
    toggle.textContent = reveal ? '隐藏' : '显示';
    input.focus();
  });

  input.addEventListener('input', clearError);

  form.addEventListener('submit', function (event) {
    event.preventDefault();
    var token = input.value.trim();
    if (!token) {
      showError('请输入访问令牌');
      input.focus();
      return;
    }

    clearError();
    submit.disabled = true;
    submit.textContent = '登录中…';

    fetch('/api/login', {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ token: token })
    })
      .then(function (res) {
        return res.json().catch(function () { return null; }).then(function (payload) {
          return { status: res.status, payload: payload };
        });
      })
      .then(function (result) {
        if (result.status === 200 && result.payload && result.payload.ok) {
          window.location.replace('/');
          return;
        }
        if (result.status === 429) {
          showError('登录尝试过于频繁，请 5 分钟后再试');
        } else {
          showError((result.payload && result.payload.error) || '登录失败，请重试');
        }
        input.select();
      })
      .catch(function () {
        showError('无法连接到服务，请确认服务仍在运行');
      })
      .then(function () {
        submit.disabled = false;
        submit.textContent = '登录';
      });
  });

  // 已登录则直接进入管理台
  fetch('/api/session', { credentials: 'same-origin' })
    .then(function (res) { return res.json(); })
    .then(function (payload) {
      if (payload && payload.ok && payload.data && payload.data.authenticated) {
        window.location.replace('/');
      }
    })
    .catch(function () { /* 忽略：停留在登录页 */ });
})();
