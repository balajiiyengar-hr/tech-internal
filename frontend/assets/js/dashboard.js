let allApps = [];
const appsPageSize = 25;
let appsPageVal = 1;
let appsPagination = { has_next: false, total: 0 };

function renderApps(apps) {
  const container = document.getElementById('appsContainer');
  const empty = document.getElementById('emptyState');

  if (!apps.length) {
    container.innerHTML = '';
    empty.classList.remove('d-none');
    return;
  }

  empty.classList.add('d-none');

  const grouped = apps.reduce((acc, app) => {
    const section = app.section || 'Applications';
    if (!acc[section]) acc[section] = [];
    acc[section].push(app);
    return acc;
  }, {});

  container.innerHTML = Object.entries(grouped).map(([section, sectionApps]) => `
    <h2 class="app-section-title">${section}</h2>
    <div class="app-grid mb-2">
      ${sectionApps.map(app => `
        <a class="app-card" href="${app.url}" target="_blank" rel="noopener noreferrer" data-name="${app.name.toLowerCase()}">
          <div class="app-icon">
            ${app.icon_url
              ? `<img src="${app.icon_url}" alt="">`
              : `<i class="bi bi-box-arrow-up-right text-primary"></i>`}
          </div>
          <div class="app-name">${app.name}</div>
        </a>
      `).join('')}
    </div>
  `).join('');
}

function filterApps(query) {
  const q = query.trim().toLowerCase();
  if (!q) {
    renderApps(allApps);
    return;
  }
  renderApps(allApps.filter(app =>
    app.name.toLowerCase().includes(q) ||
    (app.section || '').toLowerCase().includes(q)
  ));
}

function sortApps(mode) {
  const sorted = [...allApps];
  if (mode === 'section') {
    sorted.sort((a, b) => (a.section || '').localeCompare(b.section || '') || a.name.localeCompare(b.name));
  } else {
    sorted.sort((a, b) => a.name.localeCompare(b.name));
  }
  renderApps(sorted);
}

async function initDashboard() {
  if (!Api.requireAuth()) return;

  const user = Api.getUser();
  document.getElementById('userDisplayName').textContent = user.display_name || user.identifier;
  document.getElementById('userDomain').textContent = user.domain;

  if (user.role === 'admin') {
    document.getElementById('adminNavLink').classList.remove('d-none');
    document.getElementById('adminMenuLink').style.display = 'block';
  }

  document.getElementById('logoutBtn').addEventListener('click', () => Api.logoutAndClear());

  document.getElementById('appSearch').addEventListener('input', (e) => filterApps(e.target.value));
  document.getElementById('sortApps').addEventListener('change', (e) => sortApps(e.target.value));
  document.getElementById('appsPrevBtn').addEventListener('click', () => loadAppsPage(appsPageVal - 1));
  document.getElementById('appsNextBtn').addEventListener('click', () => loadAppsPage(appsPageVal + 1));

  try {
    await loadAppsPage(1);
  } catch (err) {
    if (err.status === 401) {
      Api.clearSession();
      window.location.href = '/';
      return;
    }
    document.getElementById('emptyState').classList.remove('d-none');
    document.getElementById('emptyState').innerHTML = '<p class="text-danger">Failed to load applications.</p>';
  }
}

async function loadAppsPage(pageVal) {
  if (pageVal < 1) return;
  const data = await Api.listApps(appsPageSize, pageVal);
  allApps = data.apps || [];
  appsPagination = data.pagination || { page_val: pageVal, page_size: appsPageSize, has_next: false };
  appsPageVal = appsPagination.page_val || pageVal;
  document.getElementById('appsPageLabel').textContent =
    `Page ${appsPageVal} · ${appsPagination.total || allApps.length} apps`;
  document.getElementById('appsPrevBtn').disabled = appsPageVal <= 1;
  document.getElementById('appsNextBtn').disabled = !appsPagination.has_next;
  renderApps(allApps);
}

initDashboard();
