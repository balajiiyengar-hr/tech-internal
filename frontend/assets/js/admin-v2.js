function showAdminAlert(message, type = 'success') {
  const alert = document.getElementById('adminAlert');
  alert.className = `alert alert-${type}`;
  alert.textContent = message;
  alert.classList.remove('d-none');
}

const pageSize = 25;
let pageVal = 1;
let organizationId = '';
let roles = [];

function roleOptions(selected) {
  return roles.map(role => `<option value="${role.key}" ${selected.includes(role.key) ? 'selected' : ''}>${role.name}</option>`).join('');
}

function renderMembers(members) {
  const tbody = document.getElementById('usersTableBody');
  tbody.innerHTML = members.map(member => `
    <tr>
      <td><strong>${member.display_name}</strong><br><small class="text-muted">${member.id}</small></td>
      <td>${(member.identities || []).map(identity => `<span class="badge text-bg-secondary me-1">${identity.type}</span>${identity.identifier}`).join('<br>')}</td>
      <td><select class="form-select form-select-sm member-role" data-id="${member.id}">${roleOptions(member.role_keys || [])}</select></td>
      <td>${member.active ? '<span class="text-success">Active</span>' : '<span class="text-danger">Disabled</span>'}</td>
      <td class="text-nowrap">
        <button class="btn btn-sm btn-outline-secondary toggle-member" data-id="${member.id}" data-active="${member.active}">${member.active ? 'Disable' : 'Enable'}</button>
        <button class="btn btn-sm btn-outline-danger delete-member" data-id="${member.id}">Delete</button>
      </td>
    </tr>`).join('');

  tbody.querySelectorAll('.member-role').forEach(select => select.addEventListener('change', async () => {
    try {
      await Api.updateMember(organizationId, select.dataset.id, { role_keys: [select.value] });
      showAdminAlert('Member role updated');
      await loadMembers();
    } catch (err) { showAdminAlert(err.message, 'danger'); await loadMembers(); }
  }));
  tbody.querySelectorAll('.toggle-member').forEach(button => button.addEventListener('click', async () => {
    try {
      await Api.updateMember(organizationId, button.dataset.id, { active: button.dataset.active !== 'true' });
      showAdminAlert('Member status updated');
      await loadMembers();
    } catch (err) { showAdminAlert(err.message, 'danger'); }
  }));
  tbody.querySelectorAll('.delete-member').forEach(button => button.addEventListener('click', async () => {
    if (!confirm('Delete this member and all login identities?')) return;
    try {
      await Api.deleteMember(organizationId, button.dataset.id);
      showAdminAlert('Member deleted');
      await loadMembers();
    } catch (err) { showAdminAlert(err.message, 'danger'); }
  }));
}

async function loadMembers(next = pageVal) {
  const data = await Api.listMembers(organizationId, pageSize, next);
  pageVal = data.pagination.page_val;
  renderMembers(data.members || []);
  document.getElementById('usersPageLabel').textContent = `Page ${pageVal} · ${data.pagination.total} members`;
  document.getElementById('usersPrevBtn').disabled = pageVal <= 1;
  document.getElementById('usersNextBtn').disabled = !data.pagination.has_next;
}

async function loadRoles() {
  const data = await Api.listRoles(organizationId);
  roles = data.roles || [];
  document.getElementById('regRole').innerHTML = roleOptions(['member']);
}

async function initAdmin() {
  if (!Api.requireAuth('/admin/login')) return;
  const user = Api.getUser();
  const roleKeys = user.roles || [];
  if (!roleKeys.includes('org_admin')) {
    Api.clearSession();
    window.location.href = '/admin/login';
    return;
  }
  organizationId = user.organization_id;
  document.getElementById('regDomain').value = user.organization || user.domain;
  document.getElementById('logoutBtn').addEventListener('click', () => Api.logoutAndClear('/admin/login'));

  document.getElementById('domainSettingsForm').addEventListener('submit', async event => {
    event.preventDefault();
    try {
      const settings = await Api.updateAdminDomain({
        email_login_enabled: document.getElementById('emailLoginEnabled').checked,
        sms_login_enabled: document.getElementById('smsLoginEnabled').checked,
      });
      document.getElementById('emailLoginEnabled').checked = settings.email_login_enabled;
      document.getElementById('smsLoginEnabled').checked = settings.sms_login_enabled;
      showAdminAlert('Organization login methods updated');
    } catch (err) { showAdminAlert(err.message, 'danger'); }
  });

  document.getElementById('registerUserForm').addEventListener('submit', async event => {
    event.preventDefault();
    const payload = {
      display_name: document.getElementById('regDisplayName').value.trim(),
      role_keys: [document.getElementById('regRole').value],
      email: document.getElementById('regEmail').value.trim(),
      password: document.getElementById('regPassword').value,
      phone: document.getElementById('regPhone').value.trim(),
    };
    try {
      await Api.createMember(organizationId, payload);
      showAdminAlert('Member created. Their login is ready.');
      event.target.reset();
      document.getElementById('regDomain').value = user.organization || user.domain;
      document.getElementById('regRole').innerHTML = roleOptions(['member']);
      await loadMembers(1);
    } catch (err) { showAdminAlert(err.message, 'danger'); }
  });

  document.getElementById('createRoleForm').addEventListener('submit', async event => {
    event.preventDefault();
    const permissions = document.getElementById('rolePermissions').value
      .split(',').map(value => value.trim()).filter(Boolean);
    try {
      await Api.createRole(organizationId, {
        key: document.getElementById('roleKey').value.trim(),
        name: document.getElementById('roleName').value.trim(),
        permissions,
      });
      showAdminAlert('Custom role created');
      event.target.reset();
      await loadRoles();
      await loadMembers();
    } catch (err) { showAdminAlert(err.message, 'danger'); }
  });

  document.getElementById('createAppForm').addEventListener('submit', async event => {
    event.preventDefault();
    try {
      await Api.createApp({
        section: document.getElementById('appSection').value.trim(),
        name: document.getElementById('appName').value.trim(),
        url: document.getElementById('appUrl').value.trim(),
      });
      showAdminAlert('Portal link added');
      event.target.reset();
    } catch (err) { showAdminAlert(err.message, 'danger'); }
  });

  document.getElementById('refreshUsersBtn').addEventListener('click', () => loadMembers());
  document.getElementById('usersPrevBtn').addEventListener('click', () => loadMembers(pageVal - 1));
  document.getElementById('usersNextBtn').addEventListener('click', () => loadMembers(pageVal + 1));
  document.getElementById('resetPasswordForm').addEventListener('submit', event => event.preventDefault());

  try {
    const settings = await Api.getAdminDomain();
    document.getElementById('emailLoginEnabled').checked = settings.email_login_enabled;
    document.getElementById('smsLoginEnabled').checked = settings.sms_login_enabled;
    await loadRoles();
    await loadMembers();
  } catch (err) { showAdminAlert(err.message, 'danger'); }
}

initAdmin();
