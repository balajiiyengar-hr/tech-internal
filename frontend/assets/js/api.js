const API_BASE = String((window.PORTAL_CONFIG && window.PORTAL_CONFIG.apiBase) || "").replace(/\/$/, "");
const API_V2_BASE = API_BASE.replace(/\/v1$/, '/v2');

if (!API_BASE) {
  console.error("PORTAL_CONFIG.apiBase is not set. Serve the UI with API_BASE_URL pointing at the remote API.");
}

const Api = {
  getToken() {
    return localStorage.getItem('portal_token');
  },

  getRefreshToken() {
    return localStorage.getItem('portal_refresh_token');
  },

  getUser() {
    const raw = localStorage.getItem('portal_user');
    return raw ? JSON.parse(raw) : null;
  },

  setSession(token, user, refreshToken = '') {
    localStorage.setItem('portal_token', token);
    localStorage.setItem('portal_user', JSON.stringify(user));
    if (refreshToken) localStorage.setItem('portal_refresh_token', refreshToken);
  },

  clearSession() {
    localStorage.removeItem('portal_token');
    localStorage.removeItem('portal_refresh_token');
    localStorage.removeItem('portal_user');
  },

  requireAuth(redirectTo = '/') {
    if (!this.getToken()) {
      window.location.href = redirectTo;
      return false;
    }
    return true;
  },

  async request(path, options = {}) {
    const headers = { 'Content-Type': 'application/json', ...(options.headers || {}) };
    const token = this.getToken();
    if (token) headers.Authorization = `Bearer ${token}`;

    const response = await fetch(`${API_BASE}${path}`, { ...options, headers });
    const data = await response.json().catch(() => ({}));

    if (!response.ok) {
      const error = new Error(data.error || 'Request failed');
      error.status = response.status;
      throw error;
    }
    return data;
  },

  getDomainSettings(domain) {
    return this.request(`/auth/domain?domain=${encodeURIComponent(domain)}`);
  },

  getAdminDomain() {
    return this.request('/admin/domain');
  },

  updateAdminDomain(payload) {
    return this.request('/admin/domain', {
      method: 'PUT',
      body: JSON.stringify(payload),
    });
  },

  loginEmail(domain, identifier, password, portal = 'member') {
    return this.request(`/auth/login/${portal}/email`, {
      method: 'POST',
      body: JSON.stringify({ organization: domain, identifier, password, portal }),
    });
  },

  getOAuthConfig() {
    return this.request('/oauth/config');
  },

  refreshToken(refreshToken = this.getRefreshToken()) {
    return this.request('/oauth/token/refresh', {
      method: 'POST',
      body: JSON.stringify({ refresh_token: refreshToken }),
    });
  },

  logout() {
    return this.request('/auth/logout', { method: 'POST' });
  },

  async logoutAndClear(redirectTo = '/') {
    try {
      if (this.getToken()) await this.logout();
    } catch (err) {
      console.warn('Server logout failed; clearing the local session.', err);
    } finally {
      this.clearSession();
      window.location.href = redirectTo;
    }
  },

  requestOtp(domain, identifier, portal = 'member') {
    return this.request('/auth/otp/request', {
      method: 'POST',
      body: JSON.stringify({ organization: domain, identifier, portal }),
    });
  },

  verifyOtp(domain, identifier, otp, portal = 'member') {
    return this.request('/auth/otp/verify', {
      method: 'POST',
      body: JSON.stringify({ organization: domain, identifier, otp, portal }),
    });
  },

  me() {
    return this.request('/me');
  },

  listApps(PageSize = 25, PageVal = 1) {
    return this.request(`/apps?pageSize=${Math.min(60, PageSize)}&page=${PageVal}`);
  },

  listUsers(PageSize = 25, PageVal = 1) {
    return this.request(`/admin/users?pageSize=${Math.min(60, PageSize)}&page=${PageVal}`);
  },

  registerUser(payload) {
    return this.request('/admin/users', {
      method: 'POST',
      body: JSON.stringify(payload),
    });
  },

  deleteUser(type, identifier) {
    return this.request(`/admin/users/${encodeURIComponent(type)}/${encodeURIComponent(identifier)}`, {
      method: 'DELETE',
    });
  },

  resetPassword(type, identifier, password) {
    return this.request(`/admin/users/${encodeURIComponent(type)}/${encodeURIComponent(identifier)}/password`, {
      method: 'PUT',
      body: JSON.stringify({ password }),
    });
  },

  createApp(payload) {
    return this.request('/admin/apps', {
      method: 'POST',
      body: JSON.stringify(payload),
    });
  },

  requestV2(path, options = {}) {
    const headers = { 'Content-Type': 'application/json', ...(options.headers || {}) };
    const token = this.getToken();
    if (token) headers.Authorization = `Bearer ${token}`;
    return fetch(`${API_V2_BASE}${path}`, { ...options, headers }).then(async response => {
      const data = await response.json().catch(() => ({}));
      if (!response.ok) { const error = new Error(data.error || 'Request failed'); error.status = response.status; throw error; }
      return data;
    });
  },

  listMembers(organizationId, pageSize = 25, pageVal = 1) {
    return this.requestV2(`/organizations/${organizationId}/members?pageSize=${Math.min(60, pageSize)}&page=${pageVal}`);
  },

  createMember(organizationId, payload) {
    return this.requestV2(`/organizations/${organizationId}/members`, { method: 'POST', body: JSON.stringify(payload) });
  },

  updateMember(organizationId, memberId, payload) {
    return this.requestV2(`/organizations/${organizationId}/members/${memberId}`, { method: 'PATCH', body: JSON.stringify(payload) });
  },

  deleteMember(organizationId, memberId) {
    return this.requestV2(`/organizations/${organizationId}/members/${memberId}`, { method: 'DELETE' });
  },

  listRoles(organizationId, pageSize = 25, pageVal = 1) {
    return this.requestV2(`/organizations/${organizationId}/roles?pageSize=${Math.min(60, pageSize)}&page=${pageVal}`);
  },

  createRole(organizationId, payload) {
    return this.requestV2(`/organizations/${organizationId}/roles`, { method: 'POST', body: JSON.stringify(payload) });
  },
};
