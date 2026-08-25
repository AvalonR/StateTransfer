<script lang="ts">
  import { invoke } from "@tauri-apps/api/core";
  import { listen } from "@tauri-apps/api/event";
  import { openUrl, openPath } from "@tauri-apps/plugin-opener";
  import { writeText } from "@tauri-apps/plugin-clipboard-manager";
  import { onMount } from "svelte";
  import {
    Bell,
    Settings,
    Info,
    CircleAlert,
    File,
    TextAlignStart,
    Link,
    Copy,
    CirclePlus,
    GlobeCheck,
    GlobeX,
    Type,
    SquarePen,
  } from "@lucide/svelte";
  import CustomSelect from "../lib/CustomSelect.svelte";

  onMount(() => {
    get_clipboard_stack();
    get_list_peers();
    get_notification_stack();
  });

  type Notification = {
    text: string;
    type: "normal" | "error";
    timestamp: string;
  };

  let notifications: Notification[] = $state([]);

  listen<Notification>("new_notification", (event) => {
    notifications.push(event.payload);
  });

  type ClipboardItem = {
    value: string;
    itemType: "text" | "filePath" | "url";
    from: string;
  };

  let clipboard: ClipboardItem[] = $state([]);

  async function get_clipboard_stack() {
    clipboard = await invoke("get_clipboard_stack");
  }

  async function copyText(value: string) {
    await writeText(value);
  }

  async function copyFile(value: string) {
    await writeText("file://" + value);
  }

  async function get_notification_stack() {
    notifications = await invoke("get_notification_stack");
  }

  type Peer = {
    id: string;
    address: string;
    name: string;
    active: boolean;
  };

  let list_of_peers: Peer[] = $state([]);

  const peerNameByID = $derived(
    new Map(list_of_peers.map((p) => [p.id, p.name])),
  );

  listen<typeof list_of_peers>("updated_list_of_peers", (event) => {
    list_of_peers = event.payload;
  });
  listen<Peer>("added_to_list_of_peers", (event) => {
    list_of_peers = [...list_of_peers, event.payload];
  });

  listen<ClipboardItem>("added_to_clipboard_stack", (event) => {
    clipboard.push(event.payload);
  });

  async function get_list_peers() {
    list_of_peers = await invoke("get_peer_stack");
  }

  let address: string = $state("");
  let port: string = $state("");

  async function connect_to_peer(event: SubmitEvent) {
    event.preventDefault();
    let full_address: string = address + ":" + port;
    let dialog = document.getElementById(
      "dialog-connect-to-peer",
    ) as HTMLDialogElement;
    dialog.close();
    await invoke("connect_to_peer", { peerAddress: full_address });
  }

  function openChangeNameDialog() {
    let dialog = document.getElementById(
      "dialog-change-peer-name",
    ) as HTMLDialogElement;
    dialog?.showModal();
  }

  let peerId: string = $state("");
  let newPeerName: string = $state("");

  async function change_peer_name(event: SubmitEvent) {
    event.preventDefault();
    await invoke("change_peer_name", { id: peerId, name: newPeerName });
    let dialog = document.getElementById(
      "dialog-change-peer-name",
    ) as HTMLDialogElement;
    dialog?.close();
    newPeerName = "";
    peerId = "";
  }

  let text: string = $state("");
  let target: string = $state("");

  async function send_text() {
    await invoke("send_text", { target: target, text: text });
  }

  let link: string = $state("");

  async function send_link() {
    await invoke("send_link", { target: target, link: link });
  }

  let path: string = $state("");

  async function send_file() {
    await invoke("send_file", { target: target, path: path });
  }

  async function open_Link(e: MouseEvent, link: string) {
    e.preventDefault();
    await openUrl(link);
  }
  async function open_Path(e: MouseEvent, path: string) {
    e.preventDefault();
    await openPath(path);
  }

  let showNotifications = $state(false);

  function openConnectToPeerDialog() {
    let dialog = document.getElementById(
      "dialog-connect-to-peer",
    ) as HTMLDialogElement;
    dialog?.showModal();
  }

  let manual_input: string = $state("");
  function openManuailInputDialog() {
    let dialog = document.getElementById(
      "dialog-manual-input",
    ) as HTMLDialogElement;
    dialog?.showModal();
  }

  let peerOptions = $derived(
    list_of_peers.map((p) => ({ label: `${p.name} (${p.id})`, value: p.id })),
  );
  const actionOptions = [
    { label: "Text", value: "text", icon: Type },
    { label: "Link", value: "link", icon: Link },
    { label: "Filepath", value: "path", icon: File },
  ];

  let selectedAction: string = $state("text");
  function handleManualInputForm(event: SubmitEvent) {
    event.preventDefault();
    switch (selectedAction) {
      case "text":
        text = manual_input;
        send_text();
        break;
      case "link":
        link = manual_input;
        send_link();
        break;
      case "path":
        path = manual_input;
        send_file();
        break;
    }
  }
</script>

<main class="container">
  <div class="top-strip">
    <div>
      <div>
        {#if list_of_peers.length > 0}
          <button onclick={openManuailInputDialog}>Manual Input</button>
        {:else}
          <span>No peers connected yet</span>
        {/if}
        <dialog id="dialog-manual-input" closedby="any">
          <form class="row" method="dialog" onsubmit={handleManualInputForm}>
            <div style="flex: 4 1 0;">
              <label for="id-select">Choose recepeint's ID</label>
              <CustomSelect options={peerOptions} bind:value={target} />
            </div>
            <div style="flex: 2 1 0;">
              <CustomSelect
                options={actionOptions}
                bind:value={selectedAction}
              />
            </div>
            <div style="flex: 2 1 0;">
              <input
                type="text"
                bind:value={manual_input}
                placeholder="Input"
                required
                style=""
              />
            </div>
            <button type="submit">Send</button>
          </form>
        </dialog>
      </div>
    </div>
    <div class="display: flex; gap: 20px;">
      <div class="popover-wrapper">
        <button
          class="icon-btn"
          onclick={() => (showNotifications = !showNotifications)}
          aria-label="Notification"
        >
          <Bell size={24} />
        </button>
        {#if showNotifications}
          <div id="notification-pop-up">
            {#each notifications as item}
              <div>
                <span>
                  {#if item.type == "normal"}
                    <Info size={16} />
                  {:else if item.type == "error"}
                    <CircleAlert size={16} />
                  {/if}
                  {item.text}
                </span><br />
                <i
                  >{Intl.DateTimeFormat("en-US", {
                    month: "short",
                    day: "numeric",
                    hour: "2-digit",
                    minute: "2-digit",
                    second: "2-digit",
                  }).format(new Date(item.timestamp.replace(" ", "T")))}</i
                >
              </div>
            {:else}
              No notifications
            {/each}
          </div>
        {/if}
      </div>
      <button class="icon-btn" aria-label="Settings">
        <Settings size={24} />
      </button>
    </div>
  </div>
  <div class="tab-container">
    <div class="tab">
      <div>
        <button type="submit" onclick={get_clipboard_stack}
          >Get Clipboard</button
        >
      </div>
      <ul class="clipboard-list">
        {#each clipboard as item}
          <li class="clipboard-item">
            <!-- {JSON.stringify(item)} -->
            {#if item.itemType == "text"}
              <TextAlignStart size={16} />
              <span
                >{item.value}
                <button
                  onclick={() => {
                    copyText(item.value);
                  }}><Copy size={14} /></button
                ></span
              >
            {:else if item.itemType == "url"}
              <Link size={16} />
              <a
                class="external-link"
                href={item.value}
                onclick={(e) => open_Link(e, item.value)}>{item.value}</a
              >
              <button
                onclick={() => {
                  copyText(item.value);
                }}><Copy size={14} /></button
              >
            {:else if item.itemType == "filePath"}
              <File size={16} />
              <a
                class="external-link"
                href={item.value}
                onclick={(e) => open_Path(e, item.value)}>{item.value}</a
              >
              <button
                onclick={() => {
                  copyFile(item.value);
                }}><Copy size={14} /></button
              >
            {/if}<br />
            <i>
              From: {peerNameByID.get(item.from)} ({item.from})
            </i>
          </li>
        {:else}
          <li>No items available.</li>
        {/each}
      </ul>
    </div>
    <div class="tab">
      <button onclick={get_list_peers}>Get Peers</button>
      <button onclick={openConnectToPeerDialog} style=";"
        ><CirclePlus size={20} /></button
      >
      <dialog id="dialog-connect-to-peer" closedby="any">
        <form class="row" method="dialog" onsubmit={connect_to_peer}>
          <input
            type="text"
            bind:value={address}
            placeholder="IP Address"
            required
            style="width: 18ch;"
          />
          <div
            style="color: var(--color-text); font-size: 1.5em; font-weight: 400;"
          >
            :
          </div>
          <input
            type="text"
            bind:value={port}
            placeholder="Port"
            required
            style="width: 5ch;"
          />
          <button type="submit">Connect to Peer</button>
        </form>
      </dialog>
      <ul class="peer-list">
        {#each list_of_peers as peer}
          <li class="peer-item">
            {#if peer.active}
              <GlobeCheck size={18} />
            {:else}
              <GlobeX size={18} />
            {/if}
            ID:{peer.id}<br />Name:{peer.name}<button
              onclick={() => {
                openChangeNameDialog();
                peerId = peer.id;
              }}><SquarePen size={16} /></button
            ><br />Address:{peer.address}<br />
          </li>
        {:else}
          <li>No peers available.</li>
        {/each}
      </ul>
      <dialog id="dialog-change-peer-name" closedby="any">
        <form class="row" method="dialog" onsubmit={change_peer_name}>
          <input
            type="text"
            placeholder="Name"
            bind:value={newPeerName}
            required
          />
          <button type="submit">OK</button>
        </form>
      </dialog>
    </div>
  </div>
</main>

<style>
  :root {
    font-family: Inter, Avenir, Helvetica, Arial, sans-serif;
    font-size: 16px;
    line-height: 24px;
    font-weight: 400;

    --color-background: #321b48;
    --color-background-muted: #281639;
    --color-primary: #625ad8;
    --color-secondary: #1f9ce4;
    --color-accent: #88f4ff;
    --color-text: #ffffff;
    --color-border: #ffffff;

    color: var(--color-text);
    background-color: var(--color-background);

    font-synthesis: none;
    text-rendering: optimizeLegibility;
    -webkit-font-smoothing: antialiased;
    -moz-osx-font-smoothing: grayscale;
    -webkit-text-size-adjust: 100%;
  }
  :global(html),
  :global(body) {
    height: 100%;
    overflow: hidden;
  }
  .top-strip {
    display: flex;
    justify-content: space-between;
    flex-shrink: 0;
    padding: 5px;
    padding-bottom: 0;
  }

  .popover-wrapper {
    position: relative;
    display: inline-flex;
  }

  #notification-pop-up {
    margin: 0;
    padding: 4px;
    min-width: 200px;
    max-height: calc(5 * 1.5em + 2rem);
    overflow-y: auto;

    position: absolute;
    top: calc(100% + 4px);
    right: 0;
    left: auto;
    border-radius: 12px;
    border: 2px solid var(--color-accent);
    background-color: var(--color-background-muted);
  }

  .icon-btn {
    color: var(--color-accent);
  }

  .container {
    margin: 0;
    display: flex;
    flex-direction: column;
    justify-content: flex-start;
    text-align: center;
    height: 100vh;
    overflow: hidden;
    box-sizing: border-box;
  }

  .row {
    display: flex;
    flex-direction: row;
    align-items: flex-end;
    gap: 5px;
    border-radius: 8px;
    padding: 12px;
  }

  .tab-container {
    display: grid;
    flex-direction: row;
    grid-template-columns: 1fr 1fr;
    flex: 1;
    min-height: 0;
    padding: 1rem;
    gap: 1rem;
    box-sizing: border-box;
  }

  .tab {
    background-color: var(--color-background-muted);
    padding: 1.5rem;
    max-width: 40vw;
    overflow-y: auto;
    border: 2px solid var(--color-accent);
    border-radius: 8px;
  }

  .clipboard-list {
    list-style: none;
    padding: 0;
    margin: 1rem 0;
    display: inline-flex;
    flex-direction: column;
    gap: 0.5rem;
  }

  .clipboard-item {
    background-color: var(--color-background);
    padding: 0.75rem 1rem;
    border-radius: 0.375rem;
    border-left: 4px solid var(--color-secondary);
  }

  dialog {
    color: var(--color-text);
    background-color: var(--color-background-muted);
    border: 2px solid var(--color-accent);
    border-radius: 8px;
  }

  .container .top-strip dialog {
    width: 80vw;
  }

  dialog::backdrop {
    background-color: rgba(0, 0, 0, 0.01);

    backdrop-filter: blur(2px);
    -webkit-backdrop-filter: blur(2px);
  }

  .peer-list {
    list-style: none;
    padding: 0;
    margin: 1rem 0;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }

  .peer-item {
    background-color: var(--color-background);
    padding: 0.75rem 1rem;
    border-radius: 0.375rem;
    border-left: 4px solid var(--color-secondary);
  }
  input {
    border-radius: 8px;
    border: 1px solid transparent;
    padding: 0.4em 0.8em;
    font-size: 1em;
    font-weight: 500;
    font-family: inherit;
    color: var(--color-text);
    background-color: var(--color-background);
    transition: border-color 0.25s;
    box-shadow: 4px 6px 6px rgba(0, 0, 0, 0.3);
  }

  button {
    border-radius: 8px;
    border: 2px solid transparent;
    padding: 0.4em 0.8em;
    font-size: 1em;
    font-weight: 500;
    font-family: inherit;
    color: var(--color-text);
    background-color: var(--color-background);
    transition: border-color 0.25s;
    box-shadow: 4px 6px 6px rgba(0, 0, 0, 0.3);
  }

  button {
    cursor: pointer;
  }

  button:hover {
    border-color: var(--color-accent);
  }
  button:active {
    border-color: var(--color-secondary);
    background-color: var(--color-background-muted);
  }
  .external-link {
    color: var(--color-secondary);
    text-decoration: underline;
    cursor: pointer;
    word-break: break-all;
  }
  .external-link:hover {
    color: var(--color-accent);
  }
</style>
