import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { runInNewContext } from 'node:vm';
import { test } from 'node:test';

const source = ['dock.js', 'main.js'].map(file =>
  readFileSync(new URL('./cairn-mod/' + file, import.meta.url), 'utf8')
    .replace(/^import .*;\n/gm, '')
    .replace(/export (async )?function /g, '$1function ')
    .replace('export default class Cairn', 'class Cairn')).join('\n')
  + '\nglobalThis.api = { Cairn, targetFrom, githubURL, dockNode, dockPlan };';

const copy = value => value === undefined ? undefined : JSON.parse(JSON.stringify(value));
const layoutFixture = () => ({ id: 'root', type: 'row', theme: 'graphite', children: [
  { id: 'spaces', type: 'block', size: { px: 180 }, content: { mod: 'spaces' } },
  { id: 'work', type: 'column', children: [
    { id: 'tabs', type: 'block', size: { px: 30 }, content: { mod: 'tabs' } },
    { id: 'terminal', type: 'block', size: { weight: 2 }, content: { tab: 'active' }, background: 'canvas' },
    { id: 'status', type: 'block', size: { px: 24 }, content: { mod: 'workspace-status' } },
  ] },
  { id: 'files', type: 'block', size: { px: 220 }, content: { mod: 'files' } },
  { id: 'changes', type: 'block', content: { mod: 'changes' }, hidden: true },
] });

function runtime(tree = layoutFixture()) {
  const rt = { shared: { layout: 'main', 'cairn.mode': 'float' }, trees: { main: copy(tree) }, builtins: new Set(['main']),
    disk: { '/app/layouts/main.json': JSON.stringify(tree) }, queue: [], events: [], errors: [],
    commands: [], overlays: new Set(['mod-cairn', 'unrelated']), closed: [], focus: null, keyboard: null };
  rt.flush = () => {
    for (const { name, args } of rt.queue.splice(0)) {
      if (name === 'layout.switch') {
        const text = rt.disk['/app/layouts/' + args.name + '.json'];
        if (!text) { rt.errors.push('missing layout'); continue; }
        rt.trees[args.name] = JSON.parse(text);
        rt.shared.layout = args.name;
        rt.overlays.clear();
      } else if (name === 'layout.clone') {
        const path = '/app/layouts/' + args.as + '.json';
        if (path in rt.disk) { rt.errors.push('clone collision'); continue; }
        rt.trees[args.as] = copy(rt.trees[args.name]);
        rt.disk[path] = JSON.stringify(rt.trees[args.as]);
        rt.shared.layout = args.as;
        rt.overlays.clear();
      } else if (name === 'layout.set') {
        if (rt.builtins.has(rt.shared.layout)) { rt.errors.push('shipped layout'); continue; }
        rt.trees[rt.shared.layout] = copy(args.layout);
        rt.disk['/app/layouts/' + rt.shared.layout + '.json'] = JSON.stringify(args.layout);
      } else if (['layout.show', 'layout.hide', 'layout.toggle'].includes(name)) {
        const node = findNode(rt.trees[rt.shared.layout], args.id);
        if (!node) { rt.errors.push('missing node'); continue; }
        node.hidden = name === 'layout.hide' || (name === 'layout.toggle' && !node.hidden);
      } else if (name === 'layout.select') {
        findNode(rt.trees[rt.shared.layout], args.stack).active = args.child;
      } else if (name === 'mod.open') {
        assert.equal(args.name, 'cairn');
        if (rt.overlays.has('mod-cairn')) rt.overlays.delete('mod-cairn');
        else rt.overlays.add('mod-cairn');
      } else assert.fail('unexpected command: ' + name);
    }
  };
  return rt;
}
async function settle(h) {
  await new Promise(setImmediate);
  h.rt.flush();
}


// GPUI elements may only be attached once and cannot be changed after attachment.
function element(kind) {
  const state = { kind, attached: false, nodes: [], attrs: {} };
  const proxy = new Proxy(state, { get(target, key) {
    if (key in target) return target[key];
    return (...args) => {
      assert.equal(state.attached, false, 'mutated an attached element');
      if (key === 'child') {
        const child = args[0];
        if (typeof child === 'object') { assert.equal(child.attached, false); child.attached = true; }
        state.nodes.push(child);
      } else state.attrs[key] = args;
      return proxy;
    };
  } });
  return proxy;
}

function harness(rt, viewProps = { overlay: 'mod-cairn' }) {
  const fresh = !rt;
  rt ||= runtime();
  const requests = [], tasks = [], timers = [];
  let terminalReads = 0, focusCalls = 0;
  let api;
  const sandbox = {
    View: class {}, binary: '/opt/my bin/cairn',
    div: () => element('div'), h_flex: () => element('row'), v_flex: () => element('column'),
    theme: () => new Proxy({}, { get: (_, key) => key }),
    props: () => viewProps,
    closeOverlay: id => { rt.closed.push(id); rt.overlays.delete(id); },
    shared: key => copy(rt.shared[key]), setShared: (key, value) => { rt.shared[key] = copy(value); },
    layout: () => copy(rt.trees[rt.shared.layout]), layouts: () => Object.keys(rt.trees),
    appHome: () => '/app',
    async readTextFile(path) {
      rt.events.push({ kind: 'read', path });
      if (rt.beforeRead) await rt.beforeRead(path);
      return rt.disk[path] ?? null;
    },
    async writeTextFile(path, text, expected) {
      rt.events.push({ kind: 'write-start', path, expected });
      assert.equal(expected, null, 'only exclusive creation is allowed');
      if (rt.beforeWrite) await rt.beforeWrite(path);
      if (path in rt.disk) throw new Error('exclusive create: already exists');
      rt.disk[path] = text;
      rt.events.push({ kind: 'write-success', path });
      if (rt.afterWrite) await rt.afterWrite(path);
    },
    run(name, args) {
      const command = { name, args: copy(args) };
      rt.commands.push(command); rt.queue.push(command);
      rt.events.push({ kind: 'queue', name });
    },
    focused: () => rt.focus,
    terminals: () => { terminalReads++; return [{ address: 'vps/t2', cwd: '/remote/repo' }]; },
    exec: (cmd, args, options) => new Promise((resolve, reject) => requests.push({ cmd, args, options, resolve, reject })),
  };
  runInNewContext(source, sandbox);
  api = sandbox.api;
  const handle = { focus() { focusCalls++; rt.keyboard = handle; }, is_focused: () => rt.keyboard === handle };
  const cx = { focus_handle: () => handle, stop_propagation() {}, timer: { every(ms, fn) { timers.push(fn); } },
    notify() {}, spawn(fn) { const task = fn(cx); tasks.push(task); } };
  const mod = new api.Cairn();
  mod.init({}, cx);
  if (fresh) rt.focus = { terminal: 't1', cwd: '/api', space: 'local' };
  return { ...api, mod, cx, rt, requests, tasks, closed: rt.closed, timers,
    focusCalls: () => focusCalls, terminalReads: () => terminalReads, focus: f => { rt.focus = f; } };
}
function findNode(tree, id) {
  if (tree.id === id) return tree;
  for (const child of tree.children || []) { const node = findNode(child, id); if (node) return node; }
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

test('Dock wraps only the active terminal and preserves other panels, with collision-safe names', () => {
  const h = harness();
  const tree = layoutFixture();
  tree.children.push({ id: 'cairn-dock', type: 'block', content: { mod: 'other' } });
  tree.children.push({ id: 'cairn-work', type: 'block', content: { mod: 'another' } });
  const original = copy(tree);
  const plan = h.dockPlan(tree, 'main', ['main', 'main-cairn', 'main-cairn-2']);
  assert.deepEqual(tree, original);
  assert.equal(plan.id, 'cairn-dock-2');
  const next = copy(plan.layout);
  for (const i of [0, 2, 3, 4, 5]) assert.deepEqual(next.children[i], original.children[i]);
  const work = findNode(next, 'work');
  assert.deepEqual(work.children[0].children[0].content, original.children[1].children[0].content);
  assert.deepEqual(work.children[2], original.children[1].children[2]);
  const row = work.children[1];
  assert.equal(row.type, 'row'); assert.equal(row.id, 'cairn-work-2');
  assert.deepEqual(row.size, { weight: 2 });
  assert.equal(row.children[0].id, 'terminal'); assert.equal(row.children[0].background, 'canvas');
  assert.equal(row.children[1].size.px, 400); assert.equal(row.divider, undefined);
  assert.deepEqual(row.children[1].content, { mod: 'cairn', props: { cairnMode: 'dock' } });
  assert.equal(next.theme, tree.theme);
});

test('floating → dock → float retains target, avoids focus stealing and prevents duplicate transitions', async () => {
  const h = harness();
  h.focus({ terminal: 'vps/t2', cwd: '/remote/repo', space: 'vps/space' });
  h.mod.follow(h.cx);
  h.focus(null); // Clicking the panel gives no terminal focus.
  const original = copy(h.rt.trees.main);
  h.mod.switchMode(h.cx);
  h.mod.switchMode(h.cx);
  await settle(h);
  assert.equal(h.rt.shared['cairn.mode'], 'dock');
  assert.deepEqual(h.rt.commands.map(c => c.name), ['layout.switch']);
  assert.deepEqual(h.rt.trees.main, original);
  assert.equal(h.rt.overlays.has('mod-cairn'), false);
  const dock = harness(h.rt, { cairnMode: 'dock' });
  assert.equal(dock.focusCalls(), 0);
  assert.equal(dock.mod.target.machine, 'vps');
  assert.equal(dock.mod.target.cwd, '/remote/repo');
  assert.equal(dock.requests.length, 1);
  dock.mod.switchMode(dock.cx);
  dock.mod.switchMode(dock.cx); // Hidden instances must not toggle the overlay again.
  await settle(dock);
  assert.equal(h.rt.shared['cairn.mode'], 'float');
  assert.equal(h.dockNode(h.rt.trees[h.rt.shared.layout]).hidden, true);
  assert.equal(h.rt.overlays.has('mod-cairn'), true);
  assert.equal(h.rt.commands.filter(c => c.name === 'mod.open').length, 1);
  const floating = harness(h.rt);
  assert.equal(floating.mod.target.terminal, 't2');
  assert.equal(floating.focusCalls(), 1);
  floating.mod.closePanel();
  assert.equal(h.rt.overlays.has('mod-cairn'), false);
  h.rt.overlays.add('mod-cairn');
  const reopened = harness(h.rt);
  assert.equal(reopened.mod.docked, false);
  assert.equal(reopened.mod.routed, false);
  assert.equal(h.rt.commands.filter(c => c.name === 'layout.switch').length, 1);
});

test('saved dock mode routes reopen to show/hide, preserving dimensions across copies and restart', async () => {
  const h = harness();
  h.mod.follow(h.cx); h.focus(null); h.mod.switchMode(h.cx);
  await settle(h);
  const node = h.dockNode(h.rt.trees[h.rt.shared.layout]);
  node.size.px = 517; // Native divider state as exposed by effective layout().
  const dock = harness(h.rt, { cairnMode: 'dock' });
  h.rt.overlays.add('unrelated');
  dock.mod.closePanel(); dock.mod.closePanel();
  await settle(dock);
  assert.equal(node.hidden, true);
  assert.equal(h.rt.overlays.has('unrelated'), true);
  for (const hidden of [false, true, false]) {
    h.rt.overlays.add('mod-cairn');
    const router = harness(h.rt);
    await settle(router);
    router.mod.render(router.cx); router.mod.render(router.cx);
    assert.equal(router.mod.routed, true);
    assert.equal(router.focusCalls(), 0); assert.equal(router.requests.length, 0);
    assert.equal(router.timers.length, 0);
    assert.equal(node.hidden, hidden); assert.equal(node.size.px, 517);
    assert.equal(h.rt.overlays.has('mod-cairn'), false);
    assert.equal(h.rt.overlays.has('unrelated'), true);
  }
  assert.equal(h.rt.commands.filter(c => c.name === 'layout.switch').length, 1);
  const restarted = runtime();
  restarted.trees = copy(h.rt.trees); restarted.shared = copy(h.rt.shared); restarted.disk = copy(h.rt.disk);
  const router = harness(restarted);
  await settle(router);
  assert.equal(router.mod.routed, true);
  assert.equal(router.requests.length, 0);
  assert.equal(router.dockNode(restarted.trees[restarted.shared.layout]).size.px, 517);
  assert.equal(restarted.commands[0].name, 'layout.toggle');
});

test('existing dock is found by marker and reused even with a different ID', async () => {
  const h = harness();
  h.rt.trees.main.children.push({ id: 'toolbar', type: 'block', content: { mod: 'cairn', props: { cairnMode: 'toolbar' } } });
  h.rt.trees.main.children.push({ id: 'renamed-node', type: 'block', hidden: true, size: { px: 480 },
    content: { mod: 'cairn', props: { cairnMode: 'dock' } } });
  h.rt.trees.main.children.push({ id: 'cairn-dock', type: 'block', content: { mod: 'files' } });
  h.mod.switchMode(h.cx);
  await settle(h);
  assert.deepEqual(h.rt.commands, [{ name: 'layout.show', args: { id: 'renamed-node' } }]);
  assert.equal(findNode(h.rt.trees.main, 'renamed-node').size.px, 480);
  assert.equal(findNode(h.rt.trees.main, 'cairn-dock').hidden, undefined);
});

test('unsupported trees fail visibly without cloning or replacing any layout', async () => {
  for (const tree of [
    { id: 'terminal', type: 'block', content: { terminal: 't1' } },
    { type: 'row', children: [
      { id: 'a', type: 'block', content: { tab: 'active' } },
      { id: 'b', type: 'block', content: { tab: 'active' } },
    ] },
    { type: 'row', children: [{ id: 'a' }, { id: 'a' }] },
  ]) {
    const rt = runtime(tree), before = copy(rt.trees);
    const h = harness(rt);
    h.mod.switchMode(h.cx);
    await settle(h);
    assert.match(h.mod.modeError, /Cannot dock/);
    assert.equal(rt.shared['cairn.mode'] || 'float', 'float');
    assert.deepEqual(rt.trees, before); assert.deepEqual(rt.commands, []);
    assert.equal(rt.overlays.has('mod-cairn'), true);
    assert.doesNotThrow(() => h.mod.render(h.cx));
    rt.shared['cairn.mode'] = 'dock';
    const reopen = harness(rt);
    await settle(reopen);
    assert.equal(reopen.mod.routeFailed, true);
    reopen.mod.switchMode(reopen.cx); // Visible Float escape hatch.
    assert.equal(rt.shared['cairn.mode'], 'float');
    assert.equal(reopen.mod.modeError, '');
  }
});

test('docked keyboard actions only run with panel focus and close only the dock', async () => {
  const h = harness();
  h.mod.switchMode(h.cx);
  await settle(h);
  h.rt.keyboard = 'terminal';
  const dock = harness(h.rt, { cairnMode: 'dock' });
  assert.equal(h.rt.keyboard, 'terminal');
  const before = dock.requests.length;
  const count = h.rt.commands.length;
  for (const keystroke of ['r', 'q', 'escape']) dock.mod.key({ keystroke }, dock.cx);
  assert.equal(dock.requests.length, before); assert.equal(h.rt.commands.length, count);
  dock.mod.handle.focus();
  dock.mod.key({ keystroke: 'q' }, dock.cx);
  assert.equal(h.rt.commands.at(-1).name, 'layout.hide');
  assert.equal(h.rt.commands.at(-1).args.id, h.dockNode(h.rt.trees[h.rt.shared.layout]).id);
  assert.equal(h.rt.closed.includes(undefined), false);
});

test('a reused hidden dock accepts the complete target handed off from floating', async () => {
  const h = harness();
  h.mod.follow(h.cx); h.focus(null); h.mod.switchMode(h.cx);
  await settle(h);
  const dock = harness(h.rt, { cairnMode: 'dock' });
  assert.equal(dock.mod.target.terminal, 't1');
  dock.mod.switchMode(dock.cx);
  await settle(dock);
  const floating = harness(h.rt);
  floating.focus({ terminal: 'vps/t2', cwd: '/other', space: 'vps/space' });
  floating.mod.follow(floating.cx); floating.focus(null);
  floating.mod.switchMode(floating.cx);
  await settle(floating);
  dock.mod.follow(dock.cx);
  assert.equal(dock.mod.target.terminal, 't2');
  assert.equal(dock.mod.target.machine, 'vps');
  assert.equal(dock.mod.target.cwd, '/other');
  assert.equal(dock.mod.data, null);
  dock.requests[0].resolve({ ...empty, stdout: '{"trail":null,"prs":[{"ref":"old dock target"}]}' });
  await dock.tasks[0];
  assert.equal(dock.mod.data, null);
  assert.equal(dock.requests.length, 2);
  assert.equal(dock.requests[1].options.on, 'vps');
  assert.equal(dock.requests[1].args.at(-1), '/other');
  dock.requests[1].resolve(empty); await dock.tasks[1];
  assert.equal(dock.mod.data.prs.length, 0);
  assert.equal(h.rt.commands.filter(c => c.name === 'layout.switch').length, 1);
});

test('compact renders respect single-use elements in floating and dock modes and place dock titles below', () => {
  const h = harness();
  const pr = { n: 1, kind: 'pr', ref: 'acme/api#12', title: 'Long title for the dock', state: 'open', ci: 'pending', stacked_on: 'acme/api#11', url: 'https://github.com/acme/api/pull/12' };
  const data = { trail: { name: 'work', done: 0, total: 2, steps: [pr, { n: 2, kind: 'text', text: 'release' }], notes: ['a note'] }, prs: [pr] };
  for (const docked of [false, true]) {
    h.mod.docked = docked;
    h.mod.follow(h.cx);
    for (const state of ['loading', 'error', 'empty', 'data']) {
      h.mod.error = state === 'error' ? 'offline' : '';
      h.mod.data = state === 'loading' ? null : state === 'empty' ? { trail: null, prs: [] } : data;
      assert.doesNotThrow(() => h.mod.render(h.cx), state);
    }
    const row = h.mod.pr(pr, 'test-pr', 120);
    if (docked) {
      assert.equal(row.kind, 'column');
      assert.equal(row.nodes[0].kind, 'row');
      assert.equal(row.nodes[1].nodes[0], pr.title);
    } else assert.equal(row.kind, 'row');
  }
});


test('hidden active-tab content is not chosen as a docking location', () => {
  const h = harness();
  const hidden = { id: 'hidden', type: 'block', hidden: true, content: { tab: 'active' } };
  const tree = layoutFixture(); tree.children.push(hidden);
  const plan = h.dockPlan(tree, 'main', ['main']);
  assert.deepEqual(copy(plan.layout.children.at(-1)), hidden);
  assert.equal(h.dockNode(plan.layout).id, plan.id);
  assert.throws(() => h.dockPlan({ type: 'row', children: [hidden] }, 'main', []), /no active-tab block/);
});

function deferred() {
  let resolve;
  const promise = new Promise(r => { resolve = r; });
  return { promise, resolve };
}

function personalRuntime() {
  const rt = runtime();
  rt.shared.layout = 'work';
  rt.trees.work = rt.trees.main;
  delete rt.trees.main;
  rt.disk = { '/app/layouts/work.json': JSON.stringify(rt.trees.work) };
  return rt;
}

test('disk collision omitted by layouts never overwrites the original personal layout', async () => {
  const rt = personalRuntime();
  const original = rt.disk['/app/layouts/work.json'];
  const omitted = '{"type":"block","content":{"mod":"missing-mod"}}';
  rt.disk['/app/layouts/work-cairn.json'] = omitted;
  assert.equal('work-cairn' in rt.trees, false); // layouts() omits this file.
  const h = harness(rt);
  h.mod.switchMode(h.cx);
  await settle(h);
  assert.equal(rt.disk['/app/layouts/work.json'], original);
  assert.equal(rt.disk['/app/layouts/work-cairn.json'], omitted);
  assert.equal(rt.shared.layout, 'work-cairn-2');
  assert.deepEqual(rt.commands, [{ name: 'layout.switch', args: { name: 'work-cairn-2' } }]);
  assert.equal(JSON.parse(rt.disk['/app/layouts/work-cairn-2.json']).cloned_from, undefined);
  assert.ok(rt.events.findIndex(e => e.kind === 'write-success') < rt.events.findIndex(e => e.kind === 'queue'));
});

test('new Dock layout avoids leftover native click state without modifying it', async () => {
  const rt = personalRuntime();
  const statePath = '/app/layouts/.state/work-cairn.json';
  const stale = JSON.stringify({ nodes: { 'cairn-dock': { hidden: true }, sidebar: { size: { px: 100 } } } });
  rt.disk[statePath] = stale;
  const h = harness(rt);
  h.mod.switchMode(h.cx);
  await settle(h);
  assert.equal(rt.shared.layout, 'work-cairn-2');
  assert.equal(rt.disk[statePath], stale);
  assert.equal(rt.disk['/app/layouts/work-cairn.json'], undefined);
  assert.equal(h.dockNode(rt.trees[rt.shared.layout]).hidden, undefined);
});

test('Dock remains floating and queues nothing until exclusive write resolves, including repeat clicks', async () => {
  const rt = personalRuntime();
  const gate = deferred();
  rt.beforeWrite = () => gate.promise;
  rt.shared['cairn.mode'] = 'float';
  const h = harness(rt);
  h.mod.switchMode(h.cx); h.mod.switchMode(h.cx);
  await settle(h);
  assert.equal(rt.events.filter(e => e.kind === 'write-start').length, 1);
  assert.deepEqual(rt.commands, []); assert.deepEqual(rt.closed, []);
  assert.equal(rt.overlays.has('mod-cairn'), true);
  assert.equal(rt.shared['cairn.mode'], 'float');
  assert.equal(rt.shared.layout, 'work');
  gate.resolve(); await settle(h);
  assert.equal(rt.shared['cairn.mode'], 'dock');
  assert.equal(rt.shared.layout, 'work-cairn');
  assert.equal(rt.commands.length, 1);
});

test('write failure and exclusive-create race keep original tree, mode and floating view', async () => {
  for (const race of [false, true]) {
    const rt = personalRuntime();
    const original = copy(rt.disk);
    rt.shared['cairn.mode'] = 'float';
    rt.beforeWrite = async path => {
      if (race) rt.disk[path] = 'racing owner';
      else throw new Error('permission denied');
    };
    const h = harness(rt);
    h.mod.switchMode(h.cx); await settle(h);
    assert.match(h.mod.modeError, race ? /already exists/ : /permission denied/);
    assert.equal(rt.shared.layout, 'work'); assert.equal(rt.shared['cairn.mode'], 'float');
    assert.equal(rt.disk['/app/layouts/work.json'], original['/app/layouts/work.json']);
    if (race) assert.equal(rt.disk['/app/layouts/work-cairn.json'], 'racing owner');
    assert.deepEqual(rt.commands, []); assert.deepEqual(rt.closed, []);
    assert.equal(rt.overlays.has('mod-cairn'), true);
    assert.equal(h.mod.switching, false);
  }
});

test('pending router does not focus or fetch; failed creation becomes a usable floating view', async () => {
  const rt = personalRuntime();
  const gate = deferred();
  rt.shared['cairn.mode'] = 'dock';
  rt.focus = { terminal: 't1b', cwd: '/repo' };
  rt.beforeWrite = async () => { await gate.promise; throw new Error('disk full'); };
  const h = harness(rt);
  await settle(h);
  assert.equal(h.focusCalls(), 0); assert.equal(h.requests.length, 0); assert.equal(h.timers.length, 0);
  assert.equal(h.mod.routing, true); assert.equal(rt.overlays.has('mod-cairn'), true);
  assert.deepEqual(rt.commands, []);
  gate.resolve(); await settle(h);
  assert.equal(h.mod.routeFailed, true); assert.match(h.mod.modeError, /disk full/);
  assert.equal(h.focusCalls(), 1); assert.equal(h.requests.length, 1);
  assert.equal(rt.shared['cairn.mode'], 'dock');
  h.mod.switchMode(h.cx);
  assert.equal(rt.shared['cairn.mode'], 'float');
  assert.equal(h.mod.modeError, ''); assert.equal(rt.overlays.has('mod-cairn'), true);
});

test('layout changes during read or write preparation prevent switching to the new copy', async () => {
  for (const phase of ['read', 'write']) {
    const rt = personalRuntime();
    const original = rt.disk['/app/layouts/work.json'];
    const gate = deferred();
    if (phase === 'read') rt.beforeRead = () => gate.promise;
    else rt.beforeWrite = () => gate.promise;
    const h = harness(rt);
    h.mod.switchMode(h.cx); await settle(h);
    rt.trees.other = layoutFixture(); rt.shared.layout = 'other';
    gate.resolve(); await settle(h);
    assert.match(h.mod.modeError, /Layout changed/);
    assert.equal(rt.shared.layout, 'other'); assert.deepEqual(rt.commands, []);
    assert.equal(rt.disk['/app/layouts/work.json'], original);
    if (phase === 'read') assert.equal(rt.events.some(e => e.kind === 'write-start'), false);
  }
});

test('unsafe source names are rejected before any file API or command runs', async () => {
  for (const name of ['../outside', 'a/b', 'a\\b', '', '.', '..', 'name.json', 'a b']) {
    const rt = personalRuntime(); rt.trees[name] = rt.trees.work; rt.shared.layout = name;
    const h = harness(rt); h.mod.switchMode(h.cx); await settle(h);
    assert.match(h.mod.modeError, /invalid current layout name/);
    assert.deepEqual(rt.events, []); assert.deepEqual(rt.commands, []);
  }
});

test('closing while exclusive creation is pending cancels the mode switch', async () => {
  const rt = personalRuntime(); const gate = deferred();
  rt.beforeWrite = () => gate.promise;
  const h = harness(rt); h.mod.switchMode(h.cx); await settle(h);
  h.mod.closePanel(); gate.resolve(); await settle(h);
  assert.deepEqual(rt.commands, []); assert.equal(rt.shared.layout, 'work');
  assert.equal(rt.overlays.has('mod-cairn'), false);
});

test('native overlay teardown cancels a pending Dock even without the close button', async () => {
  const rt = personalRuntime(); const gate = deferred();
  rt.beforeWrite = () => gate.promise;
  const h = harness(rt); h.mod.switchMode(h.cx); await settle(h);
  await h.mod.close(h.cx); gate.resolve(); await settle(h);
  assert.deepEqual(rt.commands, []);
  assert.equal(rt.shared.layout, 'work');
  assert.notEqual(rt.shared['cairn.mode'], 'dock');
});

test('selected second stack child keeps selection after being wrapped', () => {
  const h = harness();
  const tree = { id: 'root', type: 'stack', active: 'terminal', children: [
    { id: 'files', type: 'block', content: { mod: 'files' } },
    { id: 'terminal', type: 'block', content: { tab: 'active' } },
  ] };
  tree.children.push({ id: 'toolbar', type: 'block', content: { mod: 'cairn', props: { cairnMode: 'toolbar' } } });
  const plan = h.dockPlan(tree);
  assert.equal(plan.layout.active, plan.layout.children[1].id);
  assert.equal(plan.layout.children[1].type, 'row');
  assert.equal(plan.layout.children[1].children[0].id, 'terminal');
  assert.equal(tree.active, 'terminal');
});

test('stack falls back to first visible child when active is missing, stale or hidden', () => {
  const h = harness();
  for (const active of [undefined, 'stale', 'files']) {
    const tree = { id: 'root', type: 'stack', active, children: [
      { id: 'files', type: 'block', hidden: true, content: { mod: 'files' } },
      { id: 'terminal', type: 'block', content: { tab: 'active' } },
    ] };
    tree.children.push({ id: 'toolbar', type: 'block', content: { mod: 'cairn', props: { cairnMode: 'toolbar' } } });
    const plan = h.dockPlan(tree);
    assert.equal(plan.layout.active, plan.layout.children[1].id);
    assert.equal(plan.layout.children[1].children[0].id, 'terminal');
    assert.deepEqual(copy(plan.layout.children[0]), tree.children[0]);
  }
});

test('long target and trail names can shrink without displacing dock controls or progress', () => {
  const h = harness(); h.mod.docked = true;
  h.focus({ terminal: 'very-long-remote-machine-name/t1', cwd: '/repo' }); h.mod.follow(h.cx);
  h.mod.data = { trail: { name: 'a very long trail name', done: 0, total: 1, steps: [], notes: [] }, prs: [] };
  const root = h.mod.render(h.cx).nodes[0];
  const header = root.nodes[0];
  assert.deepEqual(header.nodes[1].attrs.min_w, [0]); assert.ok(header.nodes[1].attrs.truncate);
  assert.ok(header.nodes[2].attrs.flex_shrink_0); assert.ok(header.nodes[3].attrs.flex_shrink_0);
  const title = root.nodes[1].nodes[0];
  assert.deepEqual(title.nodes[0].attrs.min_w, [0]); assert.ok(title.nodes[0].attrs.truncate);
  assert.ok(title.nodes[1].attrs.flex_shrink_0);
});

test('first open defaults to Dock with toolbar, while explicitly saved Float stays floating', async () => {
  const rt = runtime(); delete rt.shared['cairn.mode'];
  rt.focus = { terminal: 'vps/t2', cwd: '/remote/repo' };
  const h = harness(rt); await settle(h);
  assert.equal(h.focusCalls(), 0); assert.equal(h.requests.length, 0); assert.equal(h.timers.length, 0);
  assert.equal(rt.shared['cairn.mode'], 'dock');
  assert.equal(rt.shared['cairn.target'].machine, 'vps');
  assert.ok(findNode(rt.trees[rt.shared.layout], 'cairn-toolbar'));
  const float = harness(runtime());
  assert.equal(float.focusCalls(), 1); assert.equal(float.rt.commands.length, 0);
});

test('toolbar composition preserves native tabs content, slot paint, drag and stack identity', () => {
  const h = harness(); const tree = layoutFixture();
  const bar = findNode(tree, 'tabs');
  Object.assign(bar, { min: 22, max: 50, window_drag: true, background: 'chrome', border: { width: 1, color: 'border' } });
  bar.content.props = { custom: 'preserved' };
  const original = copy(bar);
  tree.children[1].children[0] = { id: 'top-stack', type: 'stack', active: 'tabs', children: [bar] };
  tree.children.push({ id: 'cairn-toolbar' }, { id: 'cairn-tabs' });
  const result = h.dockPlan(tree).layout;
  const row = findNode(result, 'tabs');
  for (const key of ['id', 'size', 'min', 'max', 'window_drag', 'background', 'border']) assert.deepEqual(copy(row[key]), original[key]);
  assert.equal(findNode(result, 'top-stack').active, 'tabs');
  assert.deepEqual(copy(row.children[0].content), original.content);
  assert.equal(row.children[0].id, 'cairn-tabs-2'); assert.equal(row.children[1].id, 'cairn-toolbar-2');
  assert.equal(row.children[1].size.px, 84);
  assert.deepEqual(bar, original);
  assert.equal(h.dockPlan(result).layout, undefined);
});

test('existing dock without toolbar upgrades via exclusive new copy and keeps original intact', async () => {
  const rt = personalRuntime(); const tree = rt.trees.work;
  tree.children.push({ id: 'old-dock', type: 'block', hidden: true, size: { px: 515 }, content: { mod: 'cairn', props: { cairnMode: 'dock' } } });
  const before = copy(tree); const originalFile = rt.disk['/app/layouts/work.json'];
  const h = harness(rt); h.mod.switchMode(h.cx); await settle(h);
  assert.deepEqual(rt.trees.work, before); assert.equal(rt.disk['/app/layouts/work.json'], originalFile);
  assert.deepEqual(rt.commands.map(c => c.name), ['layout.switch']);
  const next = rt.trees[rt.shared.layout];
  assert.equal(h.dockNode(next).id, 'old-dock'); assert.equal(h.dockNode(next).size.px, 515);
  assert.equal(h.dockNode(next).hidden, undefined); assert.ok(findNode(next, 'cairn-toolbar'));
});

test('custom or ambiguous tabs fail without writes and default router offers Float', async () => {
  for (const multiple of [false, true]) {
    const tree = layoutFixture();
    if (multiple) tree.children.push({ id: 'other-tabs', type: 'block', content: { mod: 'tabs' } });
    else findNode(tree, 'tabs').content.mod = 'custom-tabs';
    const rt = runtime(tree); delete rt.shared['cairn.mode'];
    const h = harness(rt); await settle(h);
    assert.match(h.mod.modeError, /native tabs/);
    assert.equal(h.mod.routeFailed, true); assert.equal(h.focusCalls(), 1);
    assert.equal(rt.events.some(e => e.kind === 'write-start'), false); assert.deepEqual(rt.commands, []);
    h.mod.switchMode(h.cx); assert.equal(rt.shared['cairn.mode'], 'float');
  }
});

function toolbarButton(h) { return h.mod.render(h.cx).nodes[0]; }

test('toolbar has no query, timer or focus handle and invokes the existing toggle path', async () => {
  const rt = runtime(); rt.focus = { terminal: 'vps/t2', cwd: '/remote/repo' };
  const h = harness(rt, { cairnMode: 'toolbar' });
  assert.equal(h.mod.handle, undefined); assert.equal(h.focusCalls(), 0);
  assert.equal(h.requests.length, 0); assert.equal(h.timers.length, 0); assert.equal(h.tasks.length, 0);
  const button = toolbarButton(h); let stopped = false;
  button.attrs.on_mouse_down[1]({}, { stop_propagation() { stopped = true; } }); assert.ok(stopped);
  button.attrs.on_click[0]();
  assert.deepEqual(rt.commands, [{ name: 'mod.open', args: { name: 'cairn' } }]);
  assert.equal(rt.focus.terminal, 'vps/t2'); assert.equal(rt.shared['cairn.mode'], 'float');
});

test('toolbar highlights actual dock visibility including hidden ancestors and inactive stacks', () => {
  const rt = runtime(); const h = harness(rt, { cairnMode: 'toolbar' });
  const dock = { id: 'dock', type: 'block', content: { mod: 'cairn', props: { cairnMode: 'dock' } } };
  const stack = { id: 'panels', type: 'stack', active: 'dock', children: [{ id: 'other' }, dock] };
  rt.trees.main = { id: 'root', type: 'row', children: [stack] };
  assert.equal(toolbarButton(h).attrs.bg[0], 'selected');
  stack.hidden = true; assert.equal(toolbarButton(h).attrs.bg[0], 'chrome');
  stack.hidden = false; stack.active = 'other'; assert.equal(toolbarButton(h).attrs.bg[0], 'chrome');
  stack.children[0].hidden = true; stack.active = 'stale'; assert.equal(toolbarButton(h).attrs.bg[0], 'selected');
  dock.hidden = true; assert.equal(toolbarButton(h).attrs.bg[0], 'chrome');
});

test('toolbar float highlight follows live acknowledgement, native close and restart', async () => {
  const rt = runtime(); const toolbar = harness(rt, { cairnMode: 'toolbar' });
  const floating = harness(rt); floating.mod.render(floating.cx);
  assert.equal(toolbarButton(toolbar).attrs.bg[0], 'selected');
  const reload = harness(rt, { cairnMode: 'toolbar' });
  assert.equal(toolbarButton(reload).attrs.bg[0], 'chrome');
  floating.mod.render(floating.cx); assert.equal(toolbarButton(reload).attrs.bg[0], 'selected');
  const persisted = copy(rt.shared);
  await floating.mod.close(floating.cx);
  assert.equal(toolbarButton(reload).attrs.bg[0], 'chrome');
  floating.mod.render(floating.cx); assert.equal(toolbarButton(reload).attrs.bg[0], 'chrome');
  const restarted = runtime(); restarted.shared = persisted;
  const afterRestart = harness(restarted, { cairnMode: 'toolbar' });
  assert.equal(toolbarButton(afterRestart).attrs.bg[0], 'chrome');
  const reopened = harness(restarted); reopened.mod.render(reopened.cx);
  assert.equal(toolbarButton(afterRestart).attrs.bg[0], 'selected');
  reopened.mod.closePanel(); assert.equal(toolbarButton(afterRestart).attrs.bg[0], 'chrome');
});

test('persistent toolbar toggles dock then latest Float without duplicate layouts or target loss', async () => {
  const rt = runtime(); delete rt.shared['cairn.mode'];
  rt.focus = { terminal: 'vps/t2', cwd: '/remote/repo' };
  const first = harness(rt); await settle(first);
  const tree = rt.trees[rt.shared.layout]; const dock = first.dockNode(tree);
  const toolbar = harness(rt, { cairnMode: 'toolbar' });
  assert.equal(toolbarButton(toolbar).attrs.bg[0], 'selected');
  toolbarButton(toolbar).attrs.on_click[0](); await settle(toolbar);
  const router = harness(rt); await settle(router);
  assert.equal(dock.hidden, true); assert.ok(findNode(tree, 'cairn-toolbar'));
  assert.equal(toolbarButton(toolbar).attrs.bg[0], 'chrome');
  toolbarButton(toolbar).attrs.on_click[0](); await settle(toolbar);
  const router2 = harness(rt); await settle(router2); assert.equal(dock.hidden, false);
  const panel = harness(rt, { cairnMode: 'dock' }); panel.mod.switchMode(panel.cx); await settle(panel);
  rt.focus = null; const float = harness(rt); float.mod.render(float.cx);
  assert.equal(float.mod.target.machine, 'vps'); assert.equal(float.mod.target.cwd, '/remote/repo');
  assert.equal(toolbarButton(toolbar).attrs.bg[0], 'selected');
  toolbarButton(toolbar).attrs.on_click[0](); await settle(toolbar); await float.mod.close(float.cx);
  assert.equal(rt.overlays.has('mod-cairn'), false); assert.equal(toolbarButton(toolbar).attrs.bg[0], 'chrome');
  assert.equal(rt.commands.filter(c => c.name === 'layout.switch').length, 1);
});

test('toolbar reopen reveals a reused dock behind a hidden ancestor and inactive stack', async () => {
  const rt = runtime(); const initial = harness(rt);
  const tree = initial.dockPlan(rt.trees.main).layout;
  const dock = initial.dockNode(tree);
  const row = findNode(tree, 'cairn-work'); row.children.pop();
  tree.children.push({ id: 'hidden-parent', type: 'column', hidden: true, children: [
    { id: 'dock-stack', type: 'stack', active: 'other', children: [{ id: 'other' }, dock] },
  ] });
  rt.trees.main = tree; rt.shared['cairn.mode'] = 'dock';
  const toolbar = harness(rt, { cairnMode: 'toolbar' });
  assert.equal(toolbarButton(toolbar).attrs.bg[0], 'chrome');
  const router = harness(rt); await settle(router);
  assert.equal(toolbarButton(toolbar).attrs.bg[0], 'selected');
  assert.equal(rt.commands.some(c => c.name === 'layout.switch' || c.name === 'layout.set'), false);
  assert.equal(router.requests.length, 0); assert.equal(router.focusCalls(), 0);
});
