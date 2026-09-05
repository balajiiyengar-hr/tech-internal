import http from "k6/http";
import { check } from "k6";
import { Trend, Rate } from "k6/metrics";

const lat = {
  health: new Trend("lat_health", true),
  domain: new Trend("lat_domain", true),
  login: new Trend("lat_login", true),
  otp_request: new Trend("lat_otp_request", true),
  otp_verify: new Trend("lat_otp_verify", true),
  me: new Trend("lat_me", true),
  apps: new Trend("lat_apps", true),
  admin_domain_get: new Trend("lat_admin_domain_get", true),
  admin_domain_put: new Trend("lat_admin_domain_put", true),
  admin_users: new Trend("lat_admin_users", true),
  user_create: new Trend("lat_user_create", true),
  user_delete: new Trend("lat_user_delete", true),
  password: new Trend("lat_password", true),
  app_create: new Trend("lat_app_create", true),
  app_delete: new Trend("lat_app_delete", true),
};

const err = {
  health: new Rate("err_health"),
  domain: new Rate("err_domain"),
  login: new Rate("err_login"),
  otp_request: new Rate("err_otp_request"),
  otp_verify: new Rate("err_otp_verify"),
  me: new Rate("err_me"),
  apps: new Rate("err_apps"),
  admin_domain_get: new Rate("err_admin_domain_get"),
  admin_domain_put: new Rate("err_admin_domain_put"),
  admin_users: new Rate("err_admin_users"),
  user_create: new Rate("err_user_create"),
  user_delete: new Rate("err_user_delete"),
  password: new Rate("err_password"),
  app_create: new Rate("err_app_create"),
  app_delete: new Rate("err_app_delete"),
};

function observe(key, res, ok) {
  lat[key].add(res.timings.duration);
  err[key].add(!ok);
}

const BASE = __ENV.API_BASE || "http://127.0.0.1:8080/api/v1";
const JSON_HDR = { "Content-Type": "application/json" };
const DURATION = __ENV.DURATION || "20s";
const RATE = Number(__ENV.RATE || 200);

function car(exec, extraVUs) {
  return {
    executor: "constant-arrival-rate",
    rate: RATE,
    timeUnit: "1s",
    duration: DURATION,
    preAllocatedVUs: extraVUs ? extraVUs : 40,
    maxVUs: extraVUs ? extraVUs * 4 : 160,
    exec,
    gracefulStop: "5s",
  };
}

export const options = {
  summaryTrendStats: ["min", "avg", "med", "p(90)", "p(95)", "p(99)", "p(99.9)", "max"],
  thresholds: {},
  scenarios: {
    health: car("health", 30),
    domain: car("domain", 40),
    login: car("login", 250),
    otp_flow: car("otpFlow", 200),
    me: car("me", 40),
    apps: car("apps", 40),
    admin_domain_get: car("adminDomainGet", 40),
    admin_domain_put: car("adminDomainPut", 40),
    admin_users: car("adminUsers", 50),
    user_flow: car("userFlow", 80),
    password: car("passwordReset", 250),
    app_create: car("appCreate", 50),
    app_delete: car("appDelete", 40),
  },
};

function authHdr(token) {
  return { Authorization: `Bearer ${token}`, "Content-Type": "application/json" };
}

function tag(name) {
  return { tags: { name } };
}

export function setup() {
  const res = http.post(
    `${BASE}/auth/login/email`,
    JSON.stringify({
      domain: "techhr.com",
      identifier: "admin@techhr.com",
      password: "Admin@123",
    }),
    { headers: JSON_HDR }
  );
  if (res.status !== 200) {
    throw new Error(`setup login failed: ${res.status} ${res.body}`);
  }
  const token = JSON.parse(res.body).token;
  const h = authHdr(token);

  const otpUsers = [];
  // __VU is global across all concurrent scenarios, not local to otp_flow.
  // Seed above the aggregate max VU count so every OTP VU owns one account.
  for (let i = 1; i <= 2000; i++) {
    otpUsers.push([
      "POST",
      `${BASE}/admin/users`,
      JSON.stringify({
        domain: "techhr.com",
        type: "sms",
        identifier: `+1999${String(i).padStart(8, "0")}`,
        display_name: "Load OTP User",
        role: "user",
      }),
      { headers: h, responseCallback: http.expectedStatuses(201, 409) },
    ]);
  }
  for (let i = 0; i < otpUsers.length; i += 50) {
    http.batch(otpUsers.slice(i, i + 50));
  }
  http.post(
    `${BASE}/admin/users`,
    JSON.stringify({
      domain: "techhr.com",
      type: "email",
      identifier: "loadmix@techhr.com",
      password: "Admin@123",
      display_name: "Load Mix User",
      role: "user",
    }),
    { headers: h }
  );

  return { token };
}

export function health() {
  const res = http.get(`${BASE}/health`, tag("GET /health"));
  const ok = res.status === 200;
  observe("health", res, ok);
  check(res, { "GET /health 200": () => ok });
}

export function domain() {
  const res = http.get(`${BASE}/auth/domain?domain=techhr.com`, tag("GET /auth/domain"));
  const ok = res.status === 200;
  observe("domain", res, ok);
  check(res, { "GET /auth/domain 200": () => ok });
}

export function login() {
  const res = http.post(
    `${BASE}/auth/login/email`,
    JSON.stringify({
      domain: "techhr.com",
      identifier: "loadmix@techhr.com",
      password: "Admin@123",
    }),
    { headers: JSON_HDR, ...tag("POST /auth/login/email") }
  );
  const ok = res.status === 200;
  observe("login", res, ok);
  check(res, { "POST /auth/login/email 200": () => ok });
}

export function otpFlow() {
  const identifier = `+1999${String(__VU).padStart(8, "0")}`;
  const req = http.post(
    `${BASE}/auth/otp/request`,
    JSON.stringify({ domain: "techhr.com", identifier }),
    { headers: JSON_HDR, ...tag("POST /auth/otp/request") }
  );
  const reqOk = req.status === 200;
  observe("otp_request", req, reqOk);
  check(req, { "POST /auth/otp/request 200": () => reqOk });

  let code = "000000";
  try {
    const body = JSON.parse(req.body);
    if (body.dev_code) code = body.dev_code;
  } catch (e) {}

  const ver = http.post(
    `${BASE}/auth/otp/verify`,
    JSON.stringify({ domain: "techhr.com", identifier, otp: code }),
    { headers: JSON_HDR, ...tag("POST /auth/otp/verify") }
  );
  const verOk = ver.status === 200;
  observe("otp_verify", ver, verOk);
  check(ver, { "POST /auth/otp/verify 200": () => verOk });
}

export function me(data) {
  const res = http.get(`${BASE}/me`, { headers: authHdr(data.token), ...tag("GET /me") });
  const ok = res.status === 200;
  observe("me", res, ok);
  check(res, { "GET /me 200": () => ok });
}

export function apps(data) {
  const res = http.get(`${BASE}/apps`, { headers: authHdr(data.token), ...tag("GET /apps") });
  const ok = res.status === 200;
  observe("apps", res, ok);
  check(res, { "GET /apps 200": () => ok });
}

export function adminDomainGet(data) {
  const res = http.get(`${BASE}/admin/domain`, {
    headers: authHdr(data.token),
    ...tag("GET /admin/domain"),
  });
  const ok = res.status === 200;
  observe("admin_domain_get", res, ok);
  check(res, { "GET /admin/domain 200": () => ok });
}

export function adminDomainPut(data) {
  const res = http.put(
    `${BASE}/admin/domain`,
    JSON.stringify({ email_login_enabled: true, sms_login_enabled: true }),
    { headers: authHdr(data.token), ...tag("PUT /admin/domain") }
  );
  const ok = res.status === 200;
  observe("admin_domain_put", res, ok);
  check(res, { "PUT /admin/domain 200": () => ok });
}

export function adminUsers(data) {
  const res = http.get(`${BASE}/admin/users`, {
    headers: authHdr(data.token),
    ...tag("GET /admin/users"),
  });
  const ok = res.status === 200;
  observe("admin_users", res, ok);
  check(res, { "GET /admin/users 200": () => ok });
}

export function userFlow(data) {
  const id = `+1555${String(__VU).padStart(4, "0")}${String(__ITER).padStart(6, "0")}`;
  const created = http.post(
    `${BASE}/admin/users`,
    JSON.stringify({
      domain: "techhr.com",
      type: "sms",
      identifier: id,
      display_name: "mix",
      role: "user",
    }),
    { headers: authHdr(data.token), ...tag("POST /admin/users"), responseCallback: http.expectedStatuses(201, 409) }
  );
  const cOk = created.status === 201 || created.status === 409;
  observe("user_create", created, cOk);
  check(created, { "POST /admin/users 201": () => cOk });

  const del = http.del(`${BASE}/admin/users/sms/${encodeURIComponent(id)}`, null, {
    headers: authHdr(data.token),
    ...tag("DELETE /admin/users"),
    responseCallback: http.expectedStatuses(200, 404),
  });
  const dOk = del.status === 200 || del.status === 404;
  observe("user_delete", del, dOk);
  check(del, { "DELETE /admin/users 200": () => dOk });
}

export function passwordReset(data) {
  const res = http.put(
    `${BASE}/admin/users/email/${encodeURIComponent("loadmix@techhr.com")}/password`,
    JSON.stringify({ password: "Admin@123" }),
    { headers: authHdr(data.token), ...tag("PUT /admin/users/password") }
  );
  const ok = res.status === 200;
  observe("password", res, ok);
  check(res, { "PUT /admin/users/password 200": () => ok });
}

export function appCreate(data) {
  const res = http.post(
    `${BASE}/admin/apps`,
    JSON.stringify({
      section: "Load Mix",
      name: `mix-${__VU}-${__ITER}`,
      url: "https://example.com/mix",
    }),
    { headers: authHdr(data.token), ...tag("POST /admin/apps") }
  );
  const ok = res.status === 201;
  observe("app_create", res, ok);
  check(res, { "POST /admin/apps 201": () => ok });
}

export function appDelete(data) {
  const res = http.del(`${BASE}/admin/apps/00000000-0000-0000-0000-000000000000`, null, {
    headers: authHdr(data.token),
    ...tag("DELETE /admin/apps"),
    responseCallback: http.expectedStatuses(404),
  });
  const ok = res.status === 404;
  observe("app_delete", res, ok);
  check(res, { "DELETE /admin/apps 404": () => ok });
}

// Teardown runs even after a normal threshold failure and removes every row
// this script can create. SIGKILL cannot run teardown; scripts/loadtest/run-mixed.sh
// also installs a shell trap for that case.
function isLoadTestUser(user) {
  const identifier = String(user.identifier || "").toLowerCase();
  const displayName = String(user.display_name || "").toLowerCase();
  return identifier === "loadmix@techhr.com" ||
    identifier.startsWith("+1555") ||
    identifier.startsWith("+1999") ||
    displayName === "load otp user" ||
    displayName === "load mix user" ||
    displayName === "mix";
}

export function teardown(data) {
  const headers = authHdr(data.token);

  for (let pass = 0; pass < 100; pass++) {
    const res = http.get(`${BASE}/apps?PageSize=25&PageVal=1`, { headers });
    if (res.status !== 200) break;
    let apps = [];
    try { apps = JSON.parse(res.body).apps || []; } catch (e) { break; }
    const loadApps = apps.filter((app) => app.section === "Load Mix" || String(app.name).startsWith("mix-"));
    if (!loadApps.length) break;
    http.batch(loadApps.map((app) => [
      "DELETE",
      `${BASE}/admin/apps/${encodeURIComponent(app.id)}`,
      null,
      { headers, responseCallback: http.expectedStatuses(200, 404) },
    ]));
  }

  for (let pass = 0; pass < 100; pass++) {
    const res = http.get(`${BASE}/admin/users?PageSize=25&PageVal=1`, { headers });
    if (res.status !== 200) break;
    let users = [];
    try { users = JSON.parse(res.body).users || []; } catch (e) { break; }
    const loadUsers = users.filter(isLoadTestUser);
    if (!loadUsers.length) {
      const pagination = JSON.parse(res.body).pagination || {};
      if (!pagination.has_next) break;
      // Load rows may be on a later page; query that page and delete directly.
      for (let page = 2; page <= Math.ceil((pagination.total || 0) / 25); page++) {
        const paged = http.get(`${BASE}/admin/users?PageSize=25&PageVal=${page}`, { headers });
        if (paged.status !== 200) continue;
        let pageUsers = [];
        try { pageUsers = JSON.parse(paged.body).users || []; } catch (e) { continue; }
        loadUsers.push(...pageUsers.filter(isLoadTestUser));
      }
      if (!loadUsers.length) break;
    }
    http.batch(loadUsers.map((user) => [
      "DELETE",
      `${BASE}/admin/users/${encodeURIComponent(user.type)}/${encodeURIComponent(user.identifier)}`,
      null,
      { headers, responseCallback: http.expectedStatuses(200, 404) },
    ]));
  }
}
