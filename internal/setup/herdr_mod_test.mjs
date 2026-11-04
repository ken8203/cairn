import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { runInNewContext } from 'node:vm';
import { test } from 'node:test';

const source = readFileSync(new URL('./cairn-mod/main.js', import.meta.url), 'utf8')
  .replace(/^import .*;\n/gm, '')
  .replace(/export function /g, 'function ')
  .replace('export default class Cairn', 'class Cairn')
  + '\nglobalThis.api = { Cairn, targetFrom, githubURL };';

function harness() {
  const requests = [];
  const tasks = [];
  let terminalReads = 0;
  let focus = null;
  const closed = [];
  const sandbox = {
    View: class {}, binary: '/opt/my bin/cairn',
    props: () => ({ overlay: 'mod-cairn' }), closeOverlay: id => closed.push(id),
    focused: () => focus,
    terminals: () => { terminalReads++; return [{ address: 'vps/t2', cwd: '/remote/repo' }]; },
    exec: (cmd, args, options) => new Promise((resolve, reject) => requests.push({ cmd, args, options, resolve, reject })),
  };
  runInNewContext(source, sandbox);
  const cx = { focus_handle: () => ({ focus() {} }), stop_propagation() {}, timer: { every() {} }, notify() {}, spawn(fn) { const task = fn(cx); tasks.push(task); } };
  const mod = new sandbox.api.Cairn();
  mod.init({}, cx);
  focus = { terminal: 't1', cwd: '/api', space: 'local' };
  return { ...sandbox.api, mod, cx, requests, tasks, closed, terminalReads: () => terminalReads, focus: f => { focus = f; } };
}
const empty = { code: 0, stdout: '{"trail":null,"prs":[]}', stderr: '' };

test('floating panel closes only itself and refresh ignores modified keys', async () => {
  const h = harness();
  h.mod.follow(h.cx); h.requests[0].resolve(empty); await h.tasks[0];
  h.mod.key({ keystroke: 'cmd+r' }, h.cx);
  assert.equal(h.requests.length, 1);
  h.mod.key({ keystroke: 'r' }, h.cx);
  assert.equal(h.requests.length, 2);
  h.requests[1].resolve(empty); await h.tasks[1];
  h.mod.key({ keystroke: 'cmd+q' }, h.cx);
  assert.deepEqual(h.closed, []);
  h.mod.key({ keystroke: 'q' }, h.cx);
  h.mod.key({ keystroke: 'escape' }, h.cx);
  assert.deepEqual(h.closed, ['mod-cairn', 'mod-cairn']);
});

test('serial requests discard stale replies and retain the complete last target on mod focus', async () => {
  const h = harness();
  h.mod.follow(h.cx);
  assert.equal(h.requests.length, 1);
  assert.equal(h.requests[0].cmd, '/opt/my bin/cairn');
  h.focus({ terminal: 'vps/t2', cwd: '/remote/repo', space: 'vps/space' });
  h.mod.follow(h.cx);
  h.mod.refresh(h.cx);
  assert.equal(h.requests.length, 1);
  assert.equal(h.mod.data, null);
  h.requests[0].resolve({ ...empty, stdout: '{"trail":null,"prs":[{"ref":"stale"}]}' });
  await h.tasks[0];
  assert.equal(h.requests.length, 2);
  assert.equal(h.mod.data, null);
  assert.equal(h.requests[1].cmd, 'cairn');
  assert.equal(h.requests[1].options.on, 'vps');
  assert.deepEqual(Array.from(h.requests[1].args), ['show', '--pane', 'hc-t2', '--json', '--dir', '/remote/repo']);
  h.focus({ terminal: null, shown: null, space: 'local' });
  h.mod.follow(h.cx);
  assert.equal(h.mod.target.machine, 'vps');
  assert.equal(h.mod.target.cwd, '/remote/repo');
  h.requests[1].resolve(empty);
  await h.tasks[1];
  assert.equal(h.mod.data.prs.length, 0);
  assert.equal(h.mod.error, '');
});

test('a new target clears data immediately, never borrows cwd, and errors can recover', async () => {
  const h = harness();
  h.mod.follow(h.cx); h.requests[0].resolve(empty); await h.tasks[0];
  h.focus({ terminal: 't3', cwd: null, space: 'local' });
  h.mod.follow(h.cx);
  assert.equal(h.mod.data, null);
  assert.deepEqual(Array.from(h.requests[1].args), ['show', '--pane', 'hc-t3', '--json']);
  h.requests[1].reject(new Error('offline')); await h.tasks[1];
  assert.match(h.mod.error, /offline/);
  h.mod.refresh(h.cx); h.requests[2].resolve(empty); await h.tasks[2];
  assert.equal(h.mod.error, '');
  assert.ok(h.mod.data);
  h.focus({ terminal: 'vps//t1', cwd: '/remote', space: 'vps/space' });
  h.mod.follow(h.cx);
  assert.equal(h.mod.target, null);
  assert.equal(h.mod.data, null);
  assert.equal(h.requests.length, 3);
});

test('shown terminals use their own machine and cwd; unsafe links are inert', () => {
  const h = harness();
  const target = h.targetFrom({ terminal: 't1', shown: 'vps/t2', cwd: '/wrong', space: 'local' }, [{ address: 'vps/t2', cwd: '/right' }]);
  assert.equal(target.machine, 'vps'); assert.equal(target.cwd, '/right');
  assert.equal(h.targetFrom({ terminal: 't1', shown: 'vps/t2', cwd: '/wrong' }, []).cwd, null);
  for (const url of ['https://github.com/acme/api/pull/12']) assert.equal(h.githubURL(url), true);
  for (const url of ['http://github.com/acme/api/pull/1', 'https://github.com.evil/acme/api/pull/1', 'https://github.com@evil/acme/api/pull/1', 'javascript:alert(1)', 'https://github.com/acme/api/pull/1\n', 'https://github.com/acme/api/pull/1?x=1']) assert.equal(h.githubURL(url), false, url);
});

test('local terminal blocks in remote window context query locally without listing terminals', async () => {
  const h = harness();
  h.focus({ terminal: 't1', cwd: '/local/repo', space: 'vps/space' });
  h.mod.follow(h.cx);
  assert.equal(h.requests.length, 1);
  assert.equal(h.requests[0].cmd, '/opt/my bin/cairn');
  assert.equal(h.requests[0].options.on, null);
  assert.deepEqual(Array.from(h.requests[0].args), ['show', '--pane', 'hc-t1', '--json', '--dir', '/local/repo']);
  assert.equal(h.terminalReads(), 0);
  h.requests[0].resolve(empty); await h.tasks[0];
});

test('switching A to B to A keeps the first A response stale', async () => {
  const h = harness();
  h.mod.follow(h.cx);
  const first = h.mod.target;
  h.mod.follow(h.cx);
  assert.equal(h.mod.target, first);
  h.focus({ terminal: 'vps/t9z', cwd: '/remote/repo', space: 'vps/space' });
  h.mod.follow(h.cx);
  h.focus({ terminal: 't1', cwd: '/api', space: 'local' });
  h.mod.follow(h.cx);
  assert.notEqual(h.mod.target, first);
  assert.equal(h.requests.length, 1);
  h.requests[0].resolve({ ...empty, stdout: '{"trail":null,"prs":[{"ref":"stale A"}]}' });
  await h.tasks[0];
  assert.equal(h.mod.data, null);
  assert.equal(h.requests.length, 2);
  h.requests[1].resolve(empty); await h.tasks[1];
  assert.equal(h.mod.data.prs.length, 0);
});

test('core address alphabet and malformed remote addresses', async () => {
  for (const address of ['t19', 't1b', 't1z', 't20', 't9z', 'vps/t1b', 'vps/t9z']) {
    const h = harness();
    h.focus({ terminal: address, cwd: '/repo', space: 'local' });
    h.mod.follow(h.cx);
    assert.equal(h.requests.length, 1, address);
    assert.equal(h.requests[0].options.on, address.includes('/') ? 'vps' : null);
    assert.equal(h.requests[0].args[2], 'hc-' + address.split('/').at(-1));
    h.requests[0].resolve(empty); await h.tasks[0];
  }
  for (const address of ['t1a', 't1e', 't1i', 't1l', 't1o', 't1u', 'tb1', 't01', '/t1b', 'vps//t9z', 'vps/t1b/extra', 'vps/']) {
    const h = harness();
    h.focus({ terminal: address, cwd: '/repo' });
    h.mod.follow(h.cx);
    assert.equal(h.mod.target, null, address);
    assert.equal(h.requests.length, 0, address);
  }
});
