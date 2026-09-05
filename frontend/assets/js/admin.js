function showAdminAlert(message, type = 'success') {
  const alert = document.getElementById('adminAlert');
  alert.className = `alert alert-${type}`;
  alert.textContent = message;
  alert.classList.remove('d-none');
}

function renderUsers(users) {
  const tbody = document.getElementById('usersTableBody');
  tbody.innerHTML = users.map(user => {
    const resetBtn = user.type === 'email'
      ? `<button class="btn btn-sm btn-outline-primary reset-password-btn"
           data-type="${user.type}" data-identifier="${user.identifier}">Reset password</button>`
      : '';
    return `
    <tr>
      <td><span class="badge text-bg-secondary">${user.type}</span></td>
      <td>${user.identifier}</td>
      <td>${user.role}</td>
      <td>${user.active ? '<span class="text-success">Active</span>' : '<span class="text-danger">Disabled</span>'}</td>
      <td class="text-nowrap">
        ${resetBtn}
        <button class="btn btn-sm btn-outline-danger delete-user-btn"
          data-type="${user.type}" data-identifier="${user.identifier}">Delete</button>
      </td>
    </tr>
  `;
  }).join('');

  tbody.querySelectorAll('.delete-user-btn').forEach(btn => {
    btn.addEventListener('click', async () => {
      if (!confirm('Delete this user?')) return;
      try {
        await Api.deleteUser(btn.dataset.type, btn.dataset.identifier);
        showAdminAlert('User deleted');
        loadUsers();
      } catch (err) {
        showAdminAlert(err.message, 'danger');
      }
    });
  });

  tbody.querySelectorAll('.reset-password-btn').forEach(btn => {
    btn.addEventListener('click', () => openResetPasswordModal(btn.dataset.type, btn.dataset.identifier));
  });
}

let resetPasswordTarget = null;
const usersPageSize = 25;
let usersPageVal = 1;

function openResetPasswordModal(type, identifier) {
  resetPasswordTarget = { type, identifier };
  document.getElementById('resetPasswordUserLabel').textContent = `Set a new password for ${identifier}`;
  document.getElementById('resetPasswordForm').reset();
  bootstrap.Modal.getOrCreateInstance(document.getElementById('resetPasswordModal')).show();
}

async function loadUsers(pageVal = usersPageVal) {
  if (pageVal < 1) return;
  const data = await Api.listUsers(usersPageSize, pageVal);
  const pagination = data.pagination || { page_val: pageVal, has_next: false };
  usersPageVal = pagination.page_val || pageVal;
  document.getElementById('usersPageLabel').textContent =
    `Page ${usersPageVal} · ${pagination.total || (data.users || []).length} users`;
  document.getElementById('usersPrevBtn').disabled = usersPageVal <= 1;
  document.getElementById('usersNextBtn').disabled = !pagination.has_next;
  renderUsers(data.users || []);
}

function updateRegisterFormForType() {
  const type = document.getElementById('regType').value;
  const label = document.getElementById('regIdentifierLabel');
  const passwordGroup = document.getElementById('regPasswordGroup');
  const passwordInput = document.getElementById('regPassword');

  if (type === 'sms') {
    label.textContent = 'Mobile Number';
    passwordGroup.classList.add('d-none');
    passwordInput.removeAttribute('required');
  } else {
    label.textContent = 'Email';
    passwordGroup.classList.remove('d-none');
    passwordInput.setAttribute('required', 'required');
  }
}

function applyDomainSettingsToForm(settings) {
  document.getElementById('emailLoginEnabled').checked = !!settings.email_login_enabled;
  document.getElementById('smsLoginEnabled').checked = !!settings.sms_login_enabled;

  const typeSelect = document.getElementById('regType');
  const emailOption = typeSelect.querySelector('option[value="email"]');
  const smsOption = typeSelect.querySelector('option[value="sms"]');
  emailOption.disabled = !settings.email_login_enabled;
  smsOption.disabled = !settings.sms_login_enabled;

  if (emailOption.disabled && !smsOption.disabled) {
    typeSelect.value = 'sms';
  } else if (smsOption.disabled) {
    typeSelect.value = 'email';
  }
  updateRegisterFormForType();
}

async function initAdmin() {
  if (!Api.requireAuth()) return;

  const user = Api.getUser();
  if (user.role !== 'admin') {
    window.location.href = '/dashboard.html';
    return;
  }

  document.getElementById('regDomain').value = user.domain;
  document.getElementById('logoutBtn').addEventListener('click', () => Api.logoutAndClear());
  document.getElementById('regType').addEventListener('change', updateRegisterFormForType);
  updateRegisterFormForType();

  document.getElementById('domainSettingsForm').addEventListener('submit', async (e) => {
    e.preventDefault();
    try {
      const settings = await Api.updateAdminDomain({
        email_login_enabled: document.getElementById('emailLoginEnabled').checked,
        sms_login_enabled: document.getElementById('smsLoginEnabled').checked,
      });
      applyDomainSettingsToForm(settings);
      showAdminAlert('Domain login methods updated');
    } catch (err) {
      showAdminAlert(err.message, 'danger');
    }
  });

  document.getElementById('resetPasswordForm').addEventListener('submit', async (e) => {
    e.preventDefault();
    const password = document.getElementById('resetPasswordInput').value;
    const confirm = document.getElementById('resetPasswordConfirm').value;
    if (password !== confirm) {
      showAdminAlert('Passwords do not match', 'danger');
      return;
    }
    if (!resetPasswordTarget) return;
    try {
      await Api.resetPassword(resetPasswordTarget.type, resetPasswordTarget.identifier, password);
      bootstrap.Modal.getInstance(document.getElementById('resetPasswordModal')).hide();
      document.getElementById('resetPasswordForm').reset();
      showAdminAlert(`Password reset for ${resetPasswordTarget.identifier}`);
    } catch (err) {
      showAdminAlert(err.message, 'danger');
    }
  });

  document.getElementById('registerUserForm').addEventListener('submit', async (e) => {
    e.preventDefault();
    const type = document.getElementById('regType').value;
    const payload = {
      domain: user.domain,
      type,
      identifier: document.getElementById('regIdentifier').value.trim(),
      role: document.getElementById('regRole').value,
      display_name: document.getElementById('regDisplayName').value.trim(),
    };
    if (type === 'email') {
      payload.password = document.getElementById('regPassword').value;
    }

    try {
      await Api.registerUser(payload);
      showAdminAlert('User registered successfully');
      e.target.reset();
      document.getElementById('regDomain').value = user.domain;
      loadUsers();
    } catch (err) {
      showAdminAlert(err.message, 'danger');
    }
  });

  document.getElementById('createAppForm').addEventListener('submit', async (e) => {
    e.preventDefault();
    try {
      await Api.createApp({
        section: document.getElementById('appSection').value.trim(),
        name: document.getElementById('appName').value.trim(),
        url: document.getElementById('appUrl').value.trim(),
      });
      showAdminAlert('Portal link added');
      e.target.reset();
    } catch (err) {
      showAdminAlert(err.message, 'danger');
    }
  });

  document.getElementById('refreshUsersBtn').addEventListener('click', () => loadUsers(usersPageVal));
  document.getElementById('usersPrevBtn').addEventListener('click', () => loadUsers(usersPageVal - 1));
  document.getElementById('usersNextBtn').addEventListener('click', () => loadUsers(usersPageVal + 1));

  try {
    const settings = await Api.getAdminDomain();
    applyDomainSettingsToForm(settings);
    await loadUsers();
  } catch (err) {
    showAdminAlert(err.message, 'danger');
  }
}

initAdmin();
