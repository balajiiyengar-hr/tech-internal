import http from "k6/http";
import { check } from "k6";

const BASE = __ENV.API_BASE || "http://127.0.0.1:8080/api/v1";
const TOKEN = __ENV.ACCESS_TOKEN || "";

const loginPayload = JSON.stringify({
  domain: "techhr.com",
  identifier: "admin@techhr.com",
  password: "Admin@123",
});

const jsonHeaders = { "Content-Type": "application/json" };

function scenarioFromEnv() {
  const name = __ENV.SCENARIO || "health_1k";
  const common = {
    gracefulStop: "5s",
  };

  switch (name) {
    case "health_1k":
      return {
        health_1k: {
          ...common,
          executor: "constant-arrival-rate",
          rate: 1000,
          timeUnit: "1s",
          duration: "20s",
          preAllocatedVUs: 80,
          maxVUs: 200,
        },
      };
    case "login_closed":
      return {
        login_closed: {
          ...common,
          executor: "constant-vus",
          vus: 64,
          duration: "20s",
        },
      };
    case "login_1k":
      return {
        login_1k: {
          ...common,
          executor: "constant-arrival-rate",
          rate: 1000,
          timeUnit: "1s",
          duration: "20s",
          preAllocatedVUs: 400,
          maxVUs: 1200,
        },
      };
    case "login_ramp":
      return {
        login_ramp: {
          ...common,
          executor: "ramping-arrival-rate",
          startRate: 50,
          timeUnit: "1s",
          preAllocatedVUs: 200,
          maxVUs: 1200,
          stages: [
            { target: 100, duration: "12s" },
            { target: 250, duration: "12s" },
            { target: 500, duration: "12s" },
            { target: 1000, duration: "15s" },
          ],
        },
      };
    case "apps_1k":
      return {
        apps_1k: {
          ...common,
          executor: "constant-arrival-rate",
          rate: 1000,
          timeUnit: "1s",
          duration: "20s",
          preAllocatedVUs: 80,
          maxVUs: 200,
        },
      };
    default:
      throw new Error("unknown SCENARIO=" + name);
  }
}

export const options = {
  scenarios: scenarioFromEnv(),
  summaryTrendStats: ["min", "avg", "med", "p(90)", "p(95)", "p(99)", "max"],
  thresholds: {
    http_req_failed: ["rate<0.01"],
  },
};

export default function () {
  const name = __ENV.SCENARIO || "health_1k";
  let res;

  if (name === "health_1k") {
    res = http.get(`${BASE}/health`);
    check(res, { "health 200": (r) => r.status === 200 });
    return;
  }

  if (name === "apps_1k") {
    res = http.get(`${BASE}/apps`, {
      headers: { Authorization: `Bearer ${TOKEN}` },
    });
    check(res, { "apps 200": (r) => r.status === 200 });
    return;
  }

  res = http.post(`${BASE}/auth/login/email`, loginPayload, { headers: jsonHeaders });
  check(res, {
    "login 200": (r) => r.status === 200,
    "login has token": (r) => {
      try {
        return JSON.parse(r.body).token && JSON.parse(r.body).token.length > 20;
      } catch (e) {
        return false;
      }
    },
  });
}
