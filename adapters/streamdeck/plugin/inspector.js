"use strict";
(() => {
  const field = (id) => document.getElementById(id);
  let connection, uuid, action, instance;
  let local = {}, global = {}, globalReady = false;
  const send = (event, context, payload) => connection.send(JSON.stringify({ event, context, payload, ...(event === "setSettings" ? { action } : {}) }));
  const showLocal = () => {
    field("control").value = typeof local.control === "string" ? local.control : "";
    field("label").value = typeof local.label === "string" ? local.label : "";
  };
  window.connectElgatoStreamDeckSocket = (port, registrationID, event, info, actionInfo) => {
    if (!/^\d+$/.test(port) || Number(port) < 1 || Number(port) > 65535) return;
    const selected = JSON.parse(actionInfo);
    uuid = registrationID; action = selected.action; instance = selected.context;
    local = selected.payload.settings || {}; showLocal();
    connection = new WebSocket(`ws://127.0.0.1:${port}`);
    connection.onopen = () => {
      connection.send(JSON.stringify({ event, uuid }));
      send("getGlobalSettings", uuid);
    };
    connection.onmessage = ({ data }) => {
      let message;
      try { message = JSON.parse(data); } catch { return; }
      if (message.event === "didReceiveGlobalSettings") {
        global = message.payload.settings || {}; globalReady = true;
        field("socket").value = typeof global.socket === "string" ? global.socket : "";
        field("save").disabled = false; field("status").textContent = "Connected to Stream Deck.";
      } else if (message.event === "didReceiveSettings" && message.context === instance) {
        local = message.payload.settings || {}; showLocal();
      }
    };
    connection.onclose = connection.onerror = () => {
      globalReady = false; field("save").disabled = true;
      field("status").textContent = "Stream Deck disconnected. Reopen this inspector.";
    };
  };
  field("settings").addEventListener("submit", (event) => {
    event.preventDefault();
    if (!globalReady || connection.readyState !== WebSocket.OPEN || !event.target.reportValidity()) return;
    local = { ...local, control: field("control").value.trim(), label: field("label").value.trim() };
    global = { ...global, socket: field("socket").value.trim() };
    send("setGlobalSettings", uuid, global);
    send("setSettings", instance, local);
    field("status").textContent = "Settings sent to Stream Deck.";
  });
})();
