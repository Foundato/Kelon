import default_function from "/output/scripts/mysql_default_function_autogenerated.js"
import {jUnit, textSummary} from 'https://jslib.k6.io/k6-summary/0.0.1/index.js';

export let options = {
  vus: 5,
  //iterations: 1,
  stages: [
    {duration: "1m", target: 30},
    {duration: "1m", target: 0},
  ],
  thresholds: {
    // Declare a threshold over all HTTP response times,
    // the 95th percentile should not cross 500ms
    http_req_duration: ["p(95)<500"],
  }
};

export function handleSummary(data) {
  return {
    '/output/results/mysql_junit.xml': jUnit(data),
    'stdout': textSummary(data, {indent: ' ', enableColors: true}),
  }
}

function generateJunitXML(name, data) {
  var failures = 0;
  var cases = Object.entries(data.metrics).filter(k => k[1].thresholds != undefined).flatMap((e) => Object.entries(e[1].thresholds).flatMap((s) => {
    if (s[1].ok) {
      return `<testcase name="${e[0]} + ${s[0]}" >`;
    }
    return `<testcase name="${e[0]} + ${s[0]}"><failure message="failed" ></testcase>`;
  }));
  var result = `<?xml version="1.0"?>
<testsuites tests="${cases.length}" failures="${failures}">
    <testsuite name="${name}" tests="${cases.length}" failures="${failures}">
        ${cases.join("\n")}
    </testsuite>
</testsuites>
        `
  return result
}

export default function () {
  default_function()
}

