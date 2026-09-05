function showLoginError(message) {
  const alert = document.getElementById('loginAlert');
  alert.textContent = message;
  alert.classList.remove('d-none');
}

function hideLoginError() {
  document.getElementById('loginAlert').classList.add('d-none');
}

function completeLogin(data) {
  Api.setSession(data.access_token || data.token, data.user, data.refresh_token);
  window.location.href = loginPortal() === 'admin' ? '/admin.html' : '/dashboard.html';
}

function loginPortal() {
  return document.body.dataset.portal === 'admin' ? 'admin' : 'member';
}

function currentDomain() {
  return document.getElementById('loginDomain').value.trim().toLowerCase();
}

function showTab(paneId, tabId) {
  document.querySelectorAll('.tab-pane').forEach((pane) => pane.classList.remove('show', 'active'));
  document.querySelectorAll('#loginTabs .nav-link').forEach((tab) => tab.classList.remove('active'));
  document.getElementById(paneId).classList.add('show', 'active');
  document.getElementById(tabId).classList.add('active');
}

async function loadDomainMethods() {
  hideLoginError();
  const domain = currentDomain();
  const tabs = document.getElementById('loginTabs');
  const hint = document.getElementById('domainHint');

  if (!domain) {
    tabs.classList.add('d-none');
    hint.textContent = 'Enter your organization domain to see available login methods.';
    return;
  }

  try {
    const settings = await Api.getDomainSettings(domain);
    const emailOn = !!settings.email_login_enabled;
    const smsOn = !!settings.sms_login_enabled;

    document.getElementById('emailTabItem').classList.toggle('d-none', !emailOn);
    document.getElementById('smsTabItem').classList.toggle('d-none', !smsOn);
    tabs.classList.toggle('d-none', !emailOn && !smsOn);

    if (emailOn) {
      showTab('email-pane', 'email-tab');
    } else if (smsOn) {
      showTab('sms-pane', 'sms-tab');
    }

    hint.textContent = emailOn && smsOn
      ? 'This domain supports email/password and mobile OTP.'
      : emailOn
        ? 'This domain supports email/password login.'
        : smsOn
          ? 'This domain supports mobile OTP login.'
          : 'No login methods are enabled for this domain.';
  } catch (err) {
    tabs.classList.add('d-none');
    document.getElementById('email-pane').classList.remove('show', 'active');
    document.getElementById('sms-pane').classList.remove('show', 'active');
    hint.textContent = '';
    showLoginError(err.message === 'unknown domain' ? 'Unknown domain. Check the domain name.' : err.message);
  }
}

document.getElementById('emailLoginForm').addEventListener('submit', async (e) => {
  e.preventDefault();
  hideLoginError();

  try {
    const data = await Api.loginEmail(
      currentDomain(),
      document.getElementById('emailIdentifier').value.trim(),
      document.getElementById('emailPassword').value,
      loginPortal()
    );
    completeLogin(data);
  } catch (err) {
    showLoginError(err.message);
  }
});

let otpContext = { domain: '', identifier: '' };

async function requestOtp() {
  hideLoginError();
  otpContext.domain = currentDomain();
  otpContext.identifier = document.getElementById('smsIdentifier').value.trim();

  try {
    const data = await Api.requestOtp(otpContext.domain, otpContext.identifier, loginPortal());
    document.getElementById('otpVerifyForm').classList.remove('d-none');
    const ttl = data.expires_in || 120;
    let hint = `OTP sent. Valid for ${ttl} seconds. Request a new one if it expires.`;
    if (data.dev_code) {
      hint += ` Dev mode code: ${data.dev_code}`;
      document.getElementById('otpCode').value = data.dev_code;
    }
    document.getElementById('otpHint').textContent = hint;
  } catch (err) {
    showLoginError(err.message);
  }
}

document.getElementById('otpRequestForm').addEventListener('submit', async (e) => {
  e.preventDefault();
  await requestOtp();
});

document.getElementById('resendOtpBtn').addEventListener('click', async () => {
  await requestOtp();
});

document.getElementById('otpVerifyForm').addEventListener('submit', async (e) => {
  e.preventDefault();
  hideLoginError();

  const code = document.getElementById('otpCode').value.trim();
  try {
    const data = await Api.verifyOtp(otpContext.domain, otpContext.identifier, code, loginPortal());
    completeLogin(data);
  } catch (err) {
    showLoginError(err.message);
  }
});

document.getElementById('loginDomain').addEventListener('change', loadDomainMethods);
document.getElementById('loginDomain').addEventListener('blur', loadDomainMethods);

if (Api.getToken()) {
  window.location.href = loginPortal() === 'admin' ? '/admin.html' : '/dashboard.html';
} else {
  loadDomainMethods();
}
