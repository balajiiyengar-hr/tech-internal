import http from "k6/http";
import exec from "k6/execution";
import { check } from "k6";
import { Counter, Rate, Trend } from "k6/metrics";

const BASE = __ENV.API_BASE || "http://127.0.0.1:8080/api/v1";
const RATE = Number(__ENV.RATE || 200);
const DURATION = __ENV.DURATION || "3m";
const IDENTIFIER = __ENV.TEST_IDENTIFIER || "auth-token-200@techhr.com";
const PASSWORD = __ENV.TEST_PASSWORD || "AuthLoad@200";
const LOGOUT_SESSIONS = Number(__ENV.LOGOUT_SESSIONS || 3700);
const REFRESH_SESSIONS = Number(__ENV.REFRESH_SESSIONS || 240);

const accessRate = Math.floor(RATE * 0.70);
const refreshRate = Math.floor(RATE * 0.20);
const logoutRate = RATE - accessRate - refreshRate;

const latency = {
  authtoken: new Trend("auth_flow_duration_authtoken", true),
  refreshtoken: new Trend("auth_flow_duration_refreshtoken", true),
  logout: new Trend("auth_flow_duration_logout", true),
};
const success = {
  authtoken: new Rate("auth_flow_success_authtoken"),
  refreshtoken: new Rate("auth_flow_success_refreshtoken"),
  logout: new Rate("auth_flow_success_logout"),
};
const requests = {
  authtoken: new Counter("auth_flow_requests_authtoken"),
  refreshtoken: new Counter("auth_flow_requests_refreshtoken"),
  logout: new Counter("auth_flow_requests_logout"),
};
const serverErrors = new Counter("auth_flow_5xx");
const statuses = {
  authtoken200: new Counter("auth_status_authtoken_200"),
  refresh200: new Counter("auth_status_refreshtoken_200"),
  logout204: new Counter("auth_status_logout_204"),
  other: new Counter("auth_status_other"),
};

function scenario(rate, execName, preAllocatedVUs, maxVUs) {
  return {
    executor: "constant-arrival-rate",
    rate,
    timeUnit: "1s",
    duration: DURATION,
    preAllocatedVUs,
    maxVUs,
    exec: execName,
    gracefulStop: "10s",
    tags: { profile: "auth-token-200", flow: execName },
  };
}

export const options = {
  setupTimeout: "10m",
  teardownTimeout: "2m",
  discardResponseBodies: false,
  summaryTrendStats: ["min", "avg", "med", "p(90)", "p(95)", "p(99)", "max"],
  scenarios: {
    authtoken: scenario(accessRate, "authtoken", 30, 100),
    refreshtoken: scenario(refreshRate, "refreshtoken", 40, 120),
    logout: scenario(logoutRate, "logout", 30, 100),
  },
  thresholds: {
    auth_flow_success_authtoken: ["rate>=0.99"],
    auth_flow_success_refreshtoken: ["rate>=0.99"],
    auth_flow_success_logout: ["rate>=0.99"],
    auth_flow_5xx: ["count==0"],
    dropped_iterations: ["count==0"],
  },
};

function jsonHeaders() {
  return { "Content-Type": "application/json" };
}

function authHeaders(token) {
  return {
    "Content-Type": "application/json",
    Authorization: `Bearer ${token}`,
  };
}

function loginRequest() {
  return [
    "POST",
    `${BASE}/auth/login/email`,
    JSON.stringify({
      domain: "techhr.com",
      identifier: IDENTIFIER,
      password: PASSWORD,
    }),
    { headers: jsonHeaders(), tags: { phase: "setup", name: "setup session seed" } },
  ];
}

function parsePair(response) {
  if (response.status !== 200) {
    throw new Error(`session seed failed: ${response.status} ${response.body}`);
  }
  const body = response.json();
  return { access: body.access_token || body.token, refresh: body.refresh_token };
}

export function setup() {
  const adminLogin = http.post(
    `${BASE}/auth/login/email`,
    JSON.stringify({
      domain: "techhr.com",
      identifier: "admin@techhr.com",
      password: "Admin@123",
    }),
    { headers: jsonHeaders(), tags: { phase: "setup", name: "setup admin login" } }
  );
  if (adminLogin.status !== 200) {
    throw new Error(`admin setup login failed: ${adminLogin.status} ${adminLogin.body}`);
  }
  const adminToken = adminLogin.json("access_token") || adminLogin.json("token");

  const create = http.post(
    `${BASE}/admin/users`,
    JSON.stringify({
      domain: "techhr.com",
      type: "email",
      identifier: IDENTIFIER,
      password: PASSWORD,
      display_name: "Auth Token 200 Load User",
      role: "user",
    }),
    {
      headers: authHeaders(adminToken),
      responseCallback: http.expectedStatuses(201, 409),
      tags: { phase: "setup", name: "setup test user" },
    }
  );
  if (create.status !== 201 && create.status !== 409) {
    throw new Error(`test user setup failed: ${create.status} ${create.body}`);
  }

  const total = 1 + REFRESH_SESSIONS + LOGOUT_SESSIONS;
  const pairs = [];
  for (let offset = 0; offset < total; offset += 50) {
    const batchSize = Math.min(50, total - offset);
    const responses = http.batch(Array.from({ length: batchSize }, loginRequest));
    for (const response of responses) pairs.push(parsePair(response));
  }

  return {
    identifier: IDENTIFIER,
    adminToken,
    validation: pairs[0],
    refresh: pairs.slice(1, 1 + REFRESH_SESSIONS),
    logout: pairs.slice(1 + REFRESH_SESSIONS),
  };
}

function record(flow, response, ok, expectedStatus) {
  latency[flow].add(response.timings.duration);
  success[flow].add(ok);
  requests[flow].add(1);
  if (response.status >= 500) serverErrors.add(1);
  if (response.status === expectedStatus) {
    if (flow === "authtoken") statuses.authtoken200.add(1);
    else if (flow === "refreshtoken") statuses.refresh200.add(1);
    else statuses.logout204.add(1);
  } else {
    statuses.other.add(1);
  }
}

export function authtoken(data) {
  const response = http.get(`${BASE}/me`, {
    headers: authHeaders(data.validation.access),
    tags: { name: "GET /me [authtoken]", phase: "steady" },
  });
  const ok = response.status === 200;
  record("authtoken", response, ok, 200);
  check(response, { "authtoken expected 200": () => ok });
}

let currentRefresh = "";

export function refreshtoken(data) {
  if (!currentRefresh) {
    const pair = data.refresh[(__VU - 1) % data.refresh.length];
    currentRefresh = pair.refresh;
  }
  const response = http.post(
    `${BASE}/oauth/token/refresh`,
    JSON.stringify({ refresh_token: currentRefresh }),
    {
      headers: jsonHeaders(),
      tags: { name: "POST /oauth/token/refresh [refreshtoken]", phase: "steady" },
    }
  );
  const ok = response.status === 200 && Boolean(response.json("refresh_token"));
  if (ok) currentRefresh = response.json("refresh_token");
  record("refreshtoken", response, ok, 200);
  check(response, { "refreshtoken expected 200": () => ok });
}

export function logout(data) {
  const pair = data.logout[exec.scenario.iterationInTest];
  if (!pair) {
    statuses.other.add(1);
    success.logout.add(false);
    requests.logout.add(1);
    throw new Error("logout session pool exhausted");
  }
  const response = http.post(`${BASE}/auth/logout`, null, {
    headers: authHeaders(pair.access),
    tags: { name: "POST /auth/logout [logout]", phase: "steady" },
  });
  const ok = response.status === 204;
  record("logout", response, ok, 204);
  check(response, { "logout expected 204": () => ok });
}
