<script lang="ts">
  import { Type, type IconProps } from "@lucide/svelte";
  import { Size } from "@tauri-apps/api/dpi";
  import { onMount, type Component } from "svelte";

  interface Option {
    icon?: Component<IconProps>;
    label: string;
    value: string;
  }

  let {
    options = [],
    value = $bindable(""),
    placeholder = "Select option...",
  }: {
    options: Option[];
    value?: string;
    placeholder?: string;
  } = $props();

  let isOpen = $state(false);
  let triggerRef: HTMLButtonElement;
  let popoverRef: HTMLDivElement;
  let menuStyle = $state("display: none;");

  let selectedOption = $derived(
    options.find((o) => o.value === value),
  );

  let selectedLabel = $derived(selectedOption?.label || placeholder);
  let SelectedIcon = $derived(selectedOption?.icon);

  function updateMenuPosition() {
    if (!triggerRef) return;
    const rect = triggerRef.getBoundingClientRect();

    menuStyle = `
      top: ${rect.bottom + 4}px;
      left: ${rect.left}px;
      width: ${rect.width}px;
    `;
  }

  function toggle() {
    isOpen = !isOpen;
    if (isOpen) {
      updateMenuPosition();
      popoverRef?.showPopover();
    } else {
      popoverRef?.hidePopover();
    }
  }

  function selectOption(val: string) {
    value = val;
    isOpen = false;
    popoverRef?.hidePopover();
  }

  function handleToggle(e: ToggleEvent) {
    isOpen = e.newState === "open";
  }

  onMount(() => {
    window.addEventListener("resize", updateMenuPosition);
    window.addEventListener("scroll", updateMenuPosition, true);

    return () => {
      window.removeEventListener("resize", updateMenuPosition);
      window.removeEventListener("scroll", updateMenuPosition, true);
    };
  });
</script>

<div class="custom-select-container">
  <button
    type="button"
    bind:this={triggerRef}
    class="select-trigger"
    onclick={toggle}
  >
    <SelectedIcon size={16} />
    <span>{selectedLabel}</span>
    <span class="arrow" class:open={isOpen}>▼</span>
  </button>

  <div
    popover="auto"
    bind:this={popoverRef}
    ontoggle={handleToggle}
    class="options-menu"
    style={menuStyle}
  >
    {#each options as opt (opt.value)}
      <button
        type="button"
        class="option-item"
        class:selected={opt.value === value}
        onclick={() => selectOption(opt.value)}
      >
        {const Icon = $derived(opt.icon);}
        <div>
          <Icon size={16} />
          {opt.label}
        </div>
      </button>
    {/each}
  </div>
</div>

<style>
  .custom-select-container {
    width: 100%;
  }

  .select-trigger {
    display: flex;
    justify-content: space-between;
    align-items: center;
    width: 100%;
    padding: 8px 12px;
    background-color: var(--color-background);
    color: var(--color-text);
    border: 2px solid var(--color-accent);
    border-radius: 8px;
    cursor: pointer;
  }

  .arrow {
    font-size: 0.7rem;
    transition: transform 0.2s ease;
  }

  .arrow.open {
    transform: rotate(180deg);
  }

  .options-menu[popover] {
    position: fixed;
    margin: 0;
    padding: 4px;
    inset: auto;
    max-height: 200px;
    overflow-y: auto;
    background-color: var(--color-background);
    border: 2px solid var(--color-accent);
    border-radius: 8px;
    display: flex;
    flex-direction: column;
    gap: 2px;
    box-shadow: 0 4px 12px rgba(0, 0, 0, 0.5);
  }

  .options-menu[popover]:not(:popover-open) {
    display: none;
  }

  .option-item {
    background: transparent;
    color: var(--color-text);
    border: none;
    padding: 8px 12px;
    text-align: left;
    border-radius: 4px;
    cursor: pointer;
  }

  .option-item:hover {
    background-color: var(--color-background-muted);
  }

  .option-item.selected {
    background-color: var(--color-primary);
  }
</style>
