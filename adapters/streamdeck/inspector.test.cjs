const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');

test('inspector waits for global settings and preserves unrelated settings', () => {
  const fields = Object.fromEntries(['settings', 'control', 'label', 'socket', 'save', 'status'].map(id => [id, {
    value: '', disabled: true, addEventListener(_, callback) { this.submit = callback; }, reportValidity() { return true; }
  }]));
  let socket;
  class FakeSocket {
    static OPEN = 1;
    constructor(url) { socket = this; this.url = url; this.sent = []; this.readyState = 1; }
    send(text) { this.sent.push(JSON.parse(text)); }
  }
  const window = {};
  vm.runInNewContext(fs.readFileSync(__dirname + '/plugin/inspector.js', 'utf8'), {
    window, document: { getElementById: id => fields[id] }, WebSocket: FakeSocket
  });
  window.connectElgatoStreamDeckSocket('12345', 'ui-uuid', 'registerPropertyInspector', '{}', JSON.stringify({
    action: 'local.patchbay.deckd.control', context: 'action-instance', payload: { settings: { control: 'dial-1', retained: true } }
  }));
  socket.onopen();
  assert.deepEqual(socket.sent[0], { event: 'registerPropertyInspector', uuid: 'ui-uuid' });
  assert.equal(fields.save.disabled, true);
  socket.onmessage({ data: JSON.stringify({ event: 'didReceiveGlobalSettings', payload: { settings: { socket: '/private/sock', retained: 42 } } }) });
  assert.equal(fields.save.disabled, false);
  fields.control.value = '  volume  '; fields.label.value = 'Level'; fields.socket.value = '/new/socket';
  fields.settings.submit({ preventDefault() {}, target: fields.settings });
  const [global, local] = socket.sent.slice(-2);
  assert.deepEqual(global, { event: 'setGlobalSettings', context: 'ui-uuid', payload: { socket: '/new/socket', retained: 42 } });
  assert.deepEqual(local, { event: 'setSettings', context: 'action-instance', action: 'local.patchbay.deckd.control', payload: { control: 'volume', label: 'Level', retained: true } });
  const count = socket.sent.length;
  socket.onclose(); fields.settings.submit({ preventDefault() {}, target: fields.settings });
  assert.equal(socket.sent.length, count);
  assert.equal(fields.save.disabled, true);
});
