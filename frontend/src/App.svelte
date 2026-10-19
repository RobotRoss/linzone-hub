<script lang="ts">
  import { Service } from '../bindings/github.com/patyhank/linzone-hub/internal/app/index.js';
  import { t, getLocale, setLocale, localeOptions } from './lib/i18n/index.svelte.js';

  type DeviceSummary = { index: number; path: string; model: string; product: string; manufacturer: string; serial: string; vendorId: number; productId: number; usagePage: number; usage: number; interface: number; kind: 'headset' | 'buds' | 'mouse' | 'keyboard' | string; isBuds: boolean };
  type BatteryCell = { percent: number; status: number };
  type VolumeState = { mute: number; raw: number; percent: number };
  type AmbientState = { mode: number; ambientRaw: number; ambientPercent: number; voiceFocus: boolean };
  type MouseButtonState = { buttonIndex: number; typeDef: number; keyType: number; keyCode1: number; keyCode2: number };
  type MouseState = { currentProfile: number; dpi: number; reportRateHz: number; lodLevel: number; sensorSnap: boolean; motionSync: boolean; ledBrightness: number; ledRed: number; ledGreen: number; ledBlue: number; rfStatus: number; buttons: MouseButtonState[] };
  type SimpleState = { eventId: number; value?: number | null; raw: number[] };
  type DeviceState = {
    device: DeviceSummary;
    battery: { headset?: BatteryCell | null; left?: BatteryCell | null; right?: BatteryCell | null; case?: BatteryCell | null };
    headphoneVolume?: VolumeState | null; micVolume?: VolumeState | null; ambient?: AmbientState | null; mouse?: MouseState | null; gameChatMix?: SimpleState | null; sidetone?: VolumeState | null;
    surround?: SimpleState | null; btStatus?: SimpleState | null; btSoundQuality?: SimpleState | null; btStartupMode?: SimpleState | null; autoPowerOff?: SimpleState | null; ncStartupMode?: SimpleState | null; connectionMode?: SimpleState | null; assignable?: SimpleState | null;
    warnings: string[];
  };
  type Profile = { id: number; name: string; preset: string; axis: string; eq: number[]; controls: Record<string, number | boolean> };
  // A status label is either a literal or a thunk, so translated labels re-render on language change.
  type Label = string | (() => string);
  // H9 II enumerates as this PID only when the transmitter switch is on "Other"; it has no control interface.
  const H9_II_OTHER_MODE_PID = 0x0fa9;
  type ModelIconMap = { products?: Record<string, { marketing_name: string; icon_file: string }>; pid_map?: Record<string, string> };

  const eqBands = ['31.5', '63', '125', '250', '500', '1k', '2k', '4k', '8k', '16k'];
  const eqPresets = ['FLAT', 'BASS BOOST', 'MUSIC / VIDEO', 'FPS', 'CUSTOM'];
  const eqAxes = ['STANDARD', 'IMMERSION', 'VICTORY'];
  const tabs = ['Live', 'Profiles', 'Bluetooth', 'Buttons', 'Spatial'] as const;
  const touchActions = [
    ['Mic mute', 1], ['Volume up', 2], ['Volume down', 3], ['NC / Ambient / Off', 4], ['Game up', 5], ['Chat up', 6], ['Play / Pause', 7], ['Previous', 8], ['Next', 9], ['Quick attention', 10], ['No function', 255],
  ];
  const autoPower = [0, 5, 15, 30, 60, 180];
  // the only durations the Windows app was seen to use on the H9 II
  const autoPowerH9II = [0, 15, 30, 180];
  const reportRates = [500, 1000, 2000, 4000, 8000];
  const mouseButtons = [[1, 'Left'], [2, 'Right'], [3, 'Middle'], [4, 'Side 1'], [5, 'Side 2']];
  const mouseTypeDefs = [[255, 'Default'], [0, 'Off'], [64, 'Remap']];
  const mouseKeyTypes = [[255, 'Empty'], [0, 'Mouse'], [1, 'Keyboard'], [2, 'Media']];
  const mousePresets = [
    { label: 'Default', typeDef: 255, keyType: 255, keyCode1: 0, keyCode2: 0 },
    { label: 'Off', typeDef: 0, keyType: 255, keyCode1: 0, keyCode2: 0 },
    { label: 'Mouse Left', typeDef: 64, keyType: 0, keyCode1: 1, keyCode2: 0 },
    { label: 'Mouse Right', typeDef: 64, keyType: 0, keyCode1: 2, keyCode2: 0 },
    { label: 'Mouse Middle', typeDef: 64, keyType: 0, keyCode1: 3, keyCode2: 0 },
    { label: 'Mouse Button 4', typeDef: 64, keyType: 0, keyCode1: 4, keyCode2: 0 },
    { label: 'Mouse Button 5', typeDef: 64, keyType: 0, keyCode1: 5, keyCode2: 0 },
    { label: 'Play / Pause', typeDef: 64, keyType: 2, keyCode1: 0xCD, keyCode2: 0 },
    { label: 'Previous Track', typeDef: 64, keyType: 2, keyCode1: 0xB6, keyCode2: 0 },
    { label: 'Next Track', typeDef: 64, keyType: 2, keyCode1: 0xB5, keyCode2: 0 },
    { label: 'Mute', typeDef: 64, keyType: 2, keyCode1: 0xE2, keyCode2: 0 },
    { label: 'Volume Down', typeDef: 64, keyType: 2, keyCode1: 0xEA, keyCode2: 0 },
    { label: 'Volume Up', typeDef: 64, keyType: 2, keyCode1: 0xE9, keyCode2: 0 },
  ];

  let devices: DeviceSummary[] = $state([]);
  let selectedIndex = $state(0);
  let deviceState: DeviceState | null = $state(null);
  let modelMap: ModelIconMap = $state({});
  let activeTab: (typeof tabs)[number] = $state('Live');
  let loading = $state(false);
  // Held as a thunk (not a string) so the visible status re-translates when the language is switched.
  let message = $state<() => string>(() => t('msg.ready'));
  let error = $state('');

  let headphoneVolume = $state(50), micVolume = $state(50), sidetone = $state(20), gameChatMix = $state(50);
  let ambientMode = $state(2), ambientLevel = $state(50), voiceFocus = $state(false);
  let surroundEnabled = $state(false), ncStartupMode = $state(0), btQuality = $state(0), btStartup = $state(0), autoPowerOff = $state(30), connectionMode = $state(0), incomingPermission = $state(true);
  let assignSlot = $state(0), assignAction = $state(1);
  let mouseProfile = $state(1), mouseDpi = $state(800), mouseReportRate = $state(1000), mouseLod = $state(1), mouseBrightness = $state(128), mouseRed = $state(255), mouseGreen = $state(255), mouseBlue = $state(255), mouseMotionSync = $state(false), mouseSensorSnap = $state(false);
  let mouseButtonIndex = $state(4), mouseButtonTypeDef = $state(255), mouseButtonKeyType = $state(255), mouseButtonKeyCode1 = $state(0), mouseButtonKeyCode2 = $state(0), mouseButtonPreset = $state('Default');
  let profileName = $state('Default headset profile'), selectedPreset = $state('FLAT'), selectedAxis = $state('STANDARD'), eqValues = $state(eqBands.map(() => 0));
  let profiles: Profile[] = $state([{ id: 1, name: 'Default headset profile', preset: 'FLAT', axis: 'STANDARD', eq: eqBands.map(() => 0), controls: {} }]);
  let liveTimers: Record<string, number> = {};
  let stateRequestId = 0;

  function hex(value: number): string { return `0x${value.toString(16).toUpperCase().padStart(4, '0')}`; }
  function batteryText(cell?: BatteryCell | null): string { return cell ? `${cell.percent}%` : 'N/A'; }
  function percentText(value?: number | null): string { return value === undefined || value === null ? 'N/A' : `${value}%`; }
  // Event 97 (BT status) is an enable flag: 0 = Bluetooth off, 1 = on, independent of pairing (verified on an H9 II).
  // Any other value is shown raw rather than guessed at.
  function toggleText(state?: SimpleState | null): string {
    const value = state?.value;
    if (value === undefined || value === null) return 'N/A';
    if (value === 0) return t('state.off');
    if (value === 1) return t('state.on');
    return String(value);
  }
  // The game/chat mix is a single 0-100 balance byte (sent to the headset unchanged). It is shown as two
  // shares that always add up to 100%, in the order of the "Game / Chat" label.
  // Orientation confirmed from captures of the Windows app: 0x00 = all Game, 0x64 = all Chat.
  const MIX_VALUE_IS_CHAT_SHARE = true;
  function mixText(value: number): string {
    const share = Math.round(Math.max(0, Math.min(100, Number(value) || 0)));
    const other = 100 - share;
    return MIX_VALUE_IS_CHAT_SHARE ? `${other}% / ${share}%` : `${share}% / ${other}%`;
  }
  function mixStateText(state?: SimpleState | null): string {
    return state?.value === undefined || state?.value === null ? 'N/A' : mixText(state.value);
  }
  // The H9 II's BT status is [radio, link]: link 1 means a device is connected (0 idle, 3 radio off).
  function btChipText(state?: SimpleState | null): string {
    const text = toggleText(state);
    return onH9II && state?.value === 1 && state?.raw?.[1] === 1 ? `${text} · ${t('state.connected')}` : text;
  }
  // The H9 II has no hardware mic gain; the headset only reports whether the mic is muted.
  function micStatusText(state?: VolumeState | null): string {
    if (state === undefined || state === null) return 'N/A';
    return state.mute === 1 ? t('mic.muted') : t('state.on');
  }
  function stateText(state?: SimpleState | null): string { return state?.value === undefined || state?.value === null ? 'N/A' : String(state.value); }
  function activeDevice(): DeviceSummary | null { return devices.find((d) => d.index === selectedIndex) ?? null; }
  // Any INZONE H9 II (0x0FA8 in PC mode, 0x0FA9 in "Other" mode). Its controls differ from the other headsets:
  // no mic volume (it only reports mute), BT startup is ordered Off / On / Last state, and a few controls
  // (BT sound quality, connection mode, surround, button assignment) are not answered over USB.
  const onH9II = $derived(activeDevice()?.productId === 0x0fa8 || activeDevice()?.productId === H9_II_OTHER_MODE_PID);
  function isMouseDevice(device?: DeviceSummary | null): boolean { return device?.kind === 'mouse'; }
  function mouseButtonName(index: number): string { return mouseButtons.find(([value]) => value === index)?.[1] as string ?? `Button ${index}`; }
  function mouseTypeDefText(value: number): string { return mouseTypeDefs.find(([id]) => id === value)?.[1] as string ?? `0x${value.toString(16).toUpperCase().padStart(2, '0')}`; }
  function mouseKeyTypeText(value: number): string { return mouseKeyTypes.find(([id]) => id === value)?.[1] as string ?? `0x${value.toString(16).toUpperCase().padStart(2, '0')}`; }
  function mouseKeyText(button: MouseButtonState): string { return `${mouseKeyTypeText(button.keyType)} ${button.keyCode1}/${button.keyCode2}`; }
  function hasPermissionWarning(state?: DeviceState | null): boolean {
    return Boolean(state?.warnings?.some((warning) => /permission|denied|拒絕|權限|hidraw/i.test(warning)));
  }
  function batterySummary(state?: DeviceState | null): string {
    if (!state) return 'Battery N/A';
    if (state.device.isBuds) return `L ${batteryText(state.battery.left)} · R ${batteryText(state.battery.right)} · Case ${batteryText(state.battery.case)}`;
    return `Battery ${batteryText(state.battery.headset)}`;
  }
  function clampPercent(value: number): number { return Math.max(0, Math.min(100, Number(value) || 0)); }
  function headphoneVolumeMax(): number { return activeDevice()?.isBuds ? 30 : 100; }
  function headphoneVolumeUnit(): string { return activeDevice()?.isBuds ? ' / 30' : '%'; }
  function clampHeadphoneVolume(value: number): number { return Math.max(0, Math.min(headphoneVolumeMax(), Number(value) || 0)); }

  function deviceIcon(device: DeviceSummary): string {
    const pidKey = `0x${device.productId.toString(16).toUpperCase().padStart(4, '0')}`;
    const modelKey = modelMap.pid_map?.[pidKey] ?? modelMap.pid_map?.[pidKey.toLowerCase()];
    const iconFile = modelKey ? modelMap.products?.[modelKey]?.icon_file : undefined;
    const fallback = device.kind === 'mouse' ? 'raw/resources/menu_mouse.png' : device.kind === 'keyboard' ? 'raw/resources/menu_keybord.png' : device.isBuds ? 'raw/resources/menu_buds.png' : 'raw/resources/menu_headset.png';
    return `/inzone-assets/${iconFile ?? fallback}`;
  }

  function syncControls(next: DeviceState | null) {
    if (!next) return;
    if (next.headphoneVolume) headphoneVolume = clampHeadphoneVolume(next.headphoneVolume.percent);
    if (next.micVolume) micVolume = clampPercent(next.micVolume.percent);
    if (next.sidetone) sidetone = clampPercent(next.sidetone.percent);
    if (next.gameChatMix?.value !== undefined && next.gameChatMix?.value !== null) gameChatMix = next.gameChatMix.value;
    if (next.ambient) { ambientMode = next.ambient.mode; ambientLevel = clampPercent(next.ambient.ambientPercent); voiceFocus = next.ambient.voiceFocus; }
    if (next.surround?.value !== undefined && next.surround?.value !== null) surroundEnabled = next.surround.value !== 0;
    if (next.ncStartupMode?.value !== undefined && next.ncStartupMode?.value !== null) ncStartupMode = next.ncStartupMode.value;
    if (next.btSoundQuality?.value !== undefined && next.btSoundQuality?.value !== null) btQuality = next.btSoundQuality.value;
    if (next.btStartupMode?.value !== undefined && next.btStartupMode?.value !== null) btStartup = next.btStartupMode.value;
    if (next.autoPowerOff?.value !== undefined && next.autoPowerOff?.value !== null) autoPowerOff = next.autoPowerOff.value;
    if (next.connectionMode?.value !== undefined && next.connectionMode?.value !== null) connectionMode = next.connectionMode.value;
    if (next.mouse) {
      if (next.mouse.currentProfile) mouseProfile = next.mouse.currentProfile;
      if (next.mouse.dpi) mouseDpi = next.mouse.dpi;
      if (next.mouse.reportRateHz) mouseReportRate = next.mouse.reportRateHz;
      mouseLod = next.mouse.lodLevel;
      mouseBrightness = next.mouse.ledBrightness;
      mouseRed = next.mouse.ledRed;
      mouseGreen = next.mouse.ledGreen;
      mouseBlue = next.mouse.ledBlue;
      mouseMotionSync = next.mouse.motionSync;
      mouseSensorSnap = next.mouse.sensorSnap;
      syncMouseButtonEditor(next.mouse.buttons?.[0]);
    }
  }

  function syncMouseButtonEditor(button?: MouseButtonState) {
    if (!button) return;
    mouseButtonIndex = button.buttonIndex;
    mouseButtonTypeDef = button.typeDef;
    mouseButtonKeyType = button.keyType;
    mouseButtonKeyCode1 = button.keyCode1;
    mouseButtonKeyCode2 = button.keyCode2;
    mouseButtonPreset = 'Custom';
  }

  function resetLiveControls() {
    for (const key of Object.keys(liveTimers)) window.clearTimeout(liveTimers[key]);
    liveTimers = {};
    deviceState = null;
    headphoneVolume = 0; micVolume = 0; sidetone = 0; gameChatMix = 50;
    ambientMode = 0; ambientLevel = 0; voiceFocus = false;
    surroundEnabled = false; ncStartupMode = 0; btQuality = 0; btStartup = 0; autoPowerOff = 30; connectionMode = 0;
    mouseProfile = 1; mouseDpi = 800; mouseReportRate = 1000; mouseLod = 1; mouseBrightness = 128; mouseRed = 255; mouseGreen = 255; mouseBlue = 255; mouseMotionSync = false; mouseSensorSnap = false;
    mouseButtonIndex = 4; mouseButtonTypeDef = 255; mouseButtonKeyType = 255; mouseButtonKeyCode1 = 0; mouseButtonKeyCode2 = 0; mouseButtonPreset = 'Default';
  }

  async function selectDevice(index: number) {
    if (selectedIndex === index && deviceState) return;
    selectedIndex = index;
    resetLiveControls();
    message = () => t('msg.readingState');
    await refreshState();
  }

  async function refreshDevices() {
    loading = true; error = '';
    try {
      devices = await Service.ListDevices();
      if (!devices.length) { deviceState = null; message = () => t('msg.noDevices'); return; }
      if (!devices.some((d) => d.index === selectedIndex)) selectedIndex = devices[0].index;
      await refreshState();
    } catch (err) { error = String(err); } finally { loading = false; }
  }

  async function refreshState() {
    if (!devices.length) return;
    const requestId = ++stateRequestId;
    deviceState = null;
    loading = true; error = '';
    try {
      const nextState = await Service.GetDeviceState(selectedIndex);
      if (requestId !== stateRequestId) return;
      deviceState = nextState; syncControls(nextState); const model = nextState?.device.model ?? 'device'; message = () => t('msg.loaded', { model });
    } catch (err) { error = String(err); } finally { loading = false; }
  }

  async function applyControl(label: Label, action: () => Promise<void>, refresh = true) {
    loading = true; error = '';
    try { await action(); message = () => t('msg.sent', { label: typeof label === 'function' ? label() : label }); if (refresh) await refreshState(); } catch (err) { error = String(err); } finally { loading = false; }
  }

  async function repairUdevPermissions() {
    loading = true; error = ''; message = () => t('msg.repairWaiting');
    try {
      await Service.RepairUdevPermissions();
      message = () => t('msg.repairDone');
      await refreshDevices();
    } catch (err) {
      error = String(err);
    } finally {
      loading = false;
    }
  }

  function liveApply(key: string, label: Label, action: () => Promise<void>) {
    window.clearTimeout(liveTimers[key]);
    liveTimers[key] = window.setTimeout(() => {
      void applyControl(label, action, false);
    }, 180);
  }

  function inputNumber(event: Event): number {
    return Number((event.currentTarget as HTMLInputElement).value);
  }

  function applyAmbientLive() {
    liveApply('ambient', 'Ambient/NC', () => Service.SetAmbient(selectedIndex, Number(ambientMode), Number(ambientLevel), voiceFocus));
  }

  function applySpatial() {
    void applyControl('Surround', () => Service.SetSurround(selectedIndex, surroundEnabled));
  }

  function applyMouseLEDLive() {
    liveApply('mouseLED', 'Mouse LED', () => Service.SetMouseLED(selectedIndex, Number(mouseBrightness), Number(mouseRed), Number(mouseGreen), Number(mouseBlue)));
  }

  function applyMousePreset(label: string) {
    mouseButtonPreset = label;
    const preset = mousePresets.find((item) => item.label === label);
    if (!preset) return;
    mouseButtonTypeDef = preset.typeDef;
    mouseButtonKeyType = preset.keyType;
    mouseButtonKeyCode1 = preset.keyCode1;
    mouseButtonKeyCode2 = preset.keyCode2;
  }

  function applyMouseButton() {
    void applyControl('Mouse button', () => Service.SetMouseButton(selectedIndex, Number(mouseButtonIndex), Number(mouseButtonTypeDef), Number(mouseButtonKeyType), Number(mouseButtonKeyCode1), Number(mouseButtonKeyCode2)));
  }

  function saveProfile() {
    const controls = { headphoneVolume, micVolume, sidetone, gameChatMix, ambientMode, ambientLevel, voiceFocus, surroundEnabled, ncStartupMode, btQuality, btStartup, autoPowerOff, connectionMode, mouseProfile, mouseDpi, mouseReportRate, mouseLod, mouseBrightness, mouseRed, mouseGreen, mouseBlue, mouseMotionSync, mouseSensorSnap };
    const next = { id: Date.now(), name: profileName, preset: selectedPreset, axis: selectedAxis, eq: [...eqValues], controls };
    profiles = [next, ...profiles.filter((p) => p.name !== profileName)];
    localStorage.setItem('linzone.profiles', JSON.stringify(profiles));
    const name = profileName;
    message = () => t('msg.profileSaved', { name });
  }

  function loadProfile(profile: Profile) {
    profileName = profile.name; selectedPreset = profile.preset; selectedAxis = profile.axis; eqValues = [...profile.eq];
    headphoneVolume = Number(profile.controls.headphoneVolume ?? headphoneVolume);
    micVolume = Number(profile.controls.micVolume ?? micVolume);
    sidetone = Number(profile.controls.sidetone ?? sidetone);
    gameChatMix = Number(profile.controls.gameChatMix ?? gameChatMix);
    ambientMode = Number(profile.controls.ambientMode ?? ambientMode);
    ambientLevel = Number(profile.controls.ambientLevel ?? ambientLevel);
    voiceFocus = Boolean(profile.controls.voiceFocus ?? voiceFocus);
    surroundEnabled = Boolean(profile.controls.surroundEnabled ?? surroundEnabled);
    mouseProfile = Number(profile.controls.mouseProfile ?? mouseProfile);
    mouseDpi = Number(profile.controls.mouseDpi ?? mouseDpi);
    mouseReportRate = Number(profile.controls.mouseReportRate ?? mouseReportRate);
    mouseLod = Number(profile.controls.mouseLod ?? mouseLod);
    mouseBrightness = Number(profile.controls.mouseBrightness ?? mouseBrightness);
    mouseRed = Number(profile.controls.mouseRed ?? mouseRed);
    mouseGreen = Number(profile.controls.mouseGreen ?? mouseGreen);
    mouseBlue = Number(profile.controls.mouseBlue ?? mouseBlue);
    mouseMotionSync = Boolean(profile.controls.mouseMotionSync ?? mouseMotionSync);
    mouseSensorSnap = Boolean(profile.controls.mouseSensorSnap ?? mouseSensorSnap);
    const name = profile.name;
    message = () => t('msg.profileLoaded', { name });
  }

  function exportProfiles() { const json = JSON.stringify(profiles); message = () => json; }

  $effect(() => {
    document.documentElement.lang = localeOptions.find((option) => option.id === getLocale())?.htmlLang ?? 'en';
  });

  $effect(() => {
    fetch('/inzone-assets/model_icon_map.json').then((r) => r.json()).then((value) => (modelMap = value)).catch(() => undefined);
    const saved = localStorage.getItem('linzone.profiles');
    if (saved) profiles = JSON.parse(saved);
    refreshDevices();
  });
</script>

<main class="shell">
  <section class="topbar">
    <div class="brand"><img src="/inzone-assets/resources/app_notify_icon.png" alt="" /><div><h1>LINZONE Hub</h1><p>Unofficial Linux controller for INZONE headset features</p></div></div>
    <div class="actions">
      <div class="lang-toggle" role="group" aria-label={t('lang.label')}>
        {#each localeOptions as option}
          <button class:active={getLocale() === option.id} aria-pressed={getLocale() === option.id} lang={option.htmlLang} onclick={() => setLocale(option.id)}>{option.label}</button>
        {/each}
      </div>
      <button class="primary" onclick={refreshDevices} disabled={loading}>{t('action.rescan')}</button><button onclick={refreshState} disabled={loading || devices.length === 0}>{t('action.refreshState')}</button>
    </div>
  </section>

  {#if error}<div class="banner error">{error}</div>{:else}<div class="banner">{message()}</div>{/if}
  {#if activeDevice()?.productId === H9_II_OTHER_MODE_PID}
    <div class="banner warning"><span>{t('warn.otherMode')}</span></div>
  {/if}
  {#if hasPermissionWarning(deviceState)}
    <div class="banner warning">
      <span>{t('warn.hidrawPermission')}</span>
      <button onclick={repairUdevPermissions} disabled={loading}>{t('action.fixUdev')}</button>
    </div>
  {/if}

  <div class="layout">
    <aside class="sidebar">
      <div class="panel-title"><h2>{t('sidebar.devices')}</h2><span>{devices.length}</span></div>
      {#each devices as device}
        <button class:active={selectedIndex === device.index} class="device-card" onclick={() => selectDevice(device.index)}>
          <img src={deviceIcon(device)} alt="" /><span><strong>{device.model}</strong><small>{device.product || 'INZONE device'}</small></span>
        </button>
      {:else}<p class="muted">{t('sidebar.empty')}</p>{/each}
      <div class="mini-status"><b>{activeDevice()?.model ?? 'No device'}</b><span>{batterySummary(deviceState)}</span></div>
    </aside>

    <section class="workspace">
      <nav class="tabs">{#each tabs as tab}<button class:active={activeTab === tab} onclick={() => (activeTab = tab)}>{tab}</button>{/each}</nav>

      {#if activeTab === 'Live'}
        <article class="panel hero-device">
          <img src={activeDevice() ? deviceIcon(activeDevice()!) : '/inzone-assets/raw/resources/menu_headset.png'} alt="" />
          <div><h2>{activeDevice()?.model ?? 'INZONE device'}</h2><p>{deviceState?.device.product || t('device.connected')}</p></div>
          <div class="state-pills">
            <span>{batterySummary(deviceState)}</span>
            {#if isMouseDevice(activeDevice())}
              <span>Profile {deviceState?.mouse?.currentProfile || 'N/A'}</span><span>DPI {deviceState?.mouse?.dpi || 'N/A'}</span><span>{deviceState?.mouse?.reportRateHz || 'N/A'} Hz</span>
            {:else}
              <span>BT {btChipText(deviceState?.btStatus)}</span><span>Mix {mixStateText(deviceState?.gameChatMix)}</span><span>Sidetone {percentText(deviceState?.sidetone?.percent)}</span>
            {/if}
          </div>
        </article>

        {#if isMouseDevice(activeDevice())}
          <article class="panel controls">
            <div class="panel-title"><h2>{t('mouse.controlsTitle')}</h2><span>Protocol A</span></div>
            <div class="control-grid live-only">
              <label>Profile <b>{mouseProfile}</b><input type="range" min="1" max="4" step="1" bind:value={mouseProfile} oninput={(event) => { const value = inputNumber(event); mouseProfile = value; liveApply('mouseProfile', 'Mouse profile', () => Service.SetMouseProfile(selectedIndex, value)); }} /></label>
              <label>DPI <b>{mouseDpi}</b><input type="range" min="50" max="30000" step="50" bind:value={mouseDpi} oninput={(event) => { const value = inputNumber(event); mouseDpi = value; liveApply('mouseDPI', 'Mouse DPI', () => Service.SetMouseDPI(selectedIndex, value)); }} /></label>
              <label>Polling rate <select bind:value={mouseReportRate} onchange={() => applyControl('Mouse report rate', () => Service.SetMouseReportRate(selectedIndex, Number(mouseReportRate)))}>{#each reportRates as hz}<option value={hz}>{hz} Hz</option>{/each}</select></label>
              <label>LOD <select bind:value={mouseLod} onchange={() => applyControl('Mouse LOD', () => Service.SetMouseLOD(selectedIndex, Number(mouseLod)))}><option value={0}>0.7 mm</option><option value={1}>1.0 mm</option><option value={2}>2.0 mm</option></select></label>
              <label>LED brightness <b>{mouseBrightness}</b><input type="range" min="0" max="255" bind:value={mouseBrightness} oninput={(event) => { mouseBrightness = inputNumber(event); applyMouseLEDLive(); }} /></label>
              <label>Red <b>{mouseRed}</b><input type="range" min="0" max="255" bind:value={mouseRed} oninput={(event) => { mouseRed = inputNumber(event); applyMouseLEDLive(); }} /></label>
              <label>Green <b>{mouseGreen}</b><input type="range" min="0" max="255" bind:value={mouseGreen} oninput={(event) => { mouseGreen = inputNumber(event); applyMouseLEDLive(); }} /></label>
              <label>Blue <b>{mouseBlue}</b><input type="range" min="0" max="255" bind:value={mouseBlue} oninput={(event) => { mouseBlue = inputNumber(event); applyMouseLEDLive(); }} /></label>
              <label class="check"><input type="checkbox" bind:checked={mouseMotionSync} onchange={() => applyControl('Mouse motion sync', () => Service.SetMouseMotionSync(selectedIndex, mouseMotionSync))} /> Motion Sync</label>
              <label class="check"><input type="checkbox" bind:checked={mouseSensorSnap} onchange={() => applyControl('Mouse angle snapping', () => Service.SetMouseSensorSnap(selectedIndex, mouseSensorSnap))} /> Angle snapping</label>
            </div>
            <div class="button-row"><button class="primary" onclick={() => applyControl('Save Mouse-A profile', () => Service.SaveMouseToProfile(selectedIndex))}>{t('action.writeDeviceProfile')}</button><button onclick={() => applyControl('Reset Mouse-A defaults', () => Service.ResetMouseToDefault(selectedIndex))}>{t('action.restoreDefaults')}</button></div>
          </article>
        {:else}
          <article class="panel controls">
            <div class="panel-title"><h2>{t('audio.title')}</h2><span>{t('audio.liveApply')}</span></div>
            <div class="control-grid live-only">
              <label>{t('ctl.headphoneVolume')} <b>{headphoneVolume}{headphoneVolumeUnit()}</b><input type="range" min="0" max={headphoneVolumeMax()} bind:value={headphoneVolume} oninput={(event) => { const value = inputNumber(event); headphoneVolume = value; liveApply('headphoneVolume', () => t('ctl.headphoneVolume'), () => Service.SetHeadphoneVolume(selectedIndex, value)); }} /></label>
              {#if onH9II}
                <div class="readout">{t('ctl.mic')} <b>{micStatusText(deviceState?.micVolume)}</b></div>
              {:else}
              <label>{t('ctl.micVolume')} <b>{micVolume}%</b><input type="range" min="0" max="100" bind:value={micVolume} oninput={(event) => { const value = inputNumber(event); micVolume = value; liveApply('micVolume', () => t('ctl.micVolume'), () => Service.SetMicVolume(selectedIndex, value)); }} /></label>
              {/if}
              <label>Sidetone <b>{sidetone}%</b><input type="range" min="0" max="100" step={onH9II ? 10 : 1} bind:value={sidetone} oninput={(event) => { const value = inputNumber(event); sidetone = value; liveApply('sidetone', 'Sidetone', () => Service.SetSidetone(selectedIndex, value)); }} /></label>
              <label>Game / Chat mix <b>{mixText(gameChatMix)}</b><input type="range" min="0" max="100" bind:value={gameChatMix} oninput={(event) => { const value = inputNumber(event); gameChatMix = value; liveApply('gameChatMix', 'Game / Chat mix', () => Service.SetGameChatMix(selectedIndex, value)); }} /></label>
            </div>
            {#if onH9II}<p class="muted">{t('h9ii.micNote')}</p>{/if}
          </article>

          <article class="panel">
            <div class="panel-title"><h2>Noise Canceling / Ambient</h2><span>{t('ambient.subtitle')}</span></div>
            <div class="row four">
              <label>{t('ambient.mode')}<select bind:value={ambientMode} onchange={applyAmbientLive}><option value={0}>Off</option><option value={1}>Noise canceling</option><option value={2}>Ambient</option>{#if !onH9II}<option value={3}>Custom</option>{/if}</select></label>
              <label>Ambient level <b>{ambientLevel}%</b><input type="range" min="0" max="100" bind:value={ambientLevel} oninput={(event) => { ambientLevel = inputNumber(event); applyAmbientLive(); }} /></label>
              <label class="check"><input type="checkbox" bind:checked={voiceFocus} onchange={applyAmbientLive} /> Voice focus</label>
            </div>
          </article>
        {/if}
      {/if}

      {#if activeTab === 'Profiles'}
        {#if isMouseDevice(activeDevice())}
          <article class="panel">
            <div class="panel-title"><h2>Mouse-A profile</h2><span>{t('profiles.mouseSubtitle')}</span></div>
            <div class="row three"><label>Current profile<select bind:value={mouseProfile} onchange={() => applyControl('Mouse profile', () => Service.SetMouseProfile(selectedIndex, Number(mouseProfile)))}><option value={1}>Profile 1</option><option value={2}>Profile 2</option><option value={3}>Profile 3</option><option value={4}>Profile 4</option></select></label><button class="primary" onclick={() => applyControl('Save Mouse-A profile', () => Service.SaveMouseToProfile(selectedIndex))}>{t('profiles.writeCurrent')}</button><button onclick={() => applyControl('Reset Mouse-A defaults', () => Service.ResetMouseToDefault(selectedIndex))}>{t('action.restoreDefaults')}</button></div>
            <p class="muted">{t('profiles.mouseNote')}</p>
          </article>
        {:else}
          <article class="panel">
            <div class="panel-title"><h2>Profiles / 10-band EQ</h2><span>{t('profiles.subtitle')}</span></div>
            <div class="row four"><input bind:value={profileName} aria-label="profile name" /><select bind:value={selectedPreset}>{#each eqPresets as preset}<option>{preset}</option>{/each}</select><select bind:value={selectedAxis}>{#each eqAxes as axis}<option>{axis}</option>{/each}</select><button class="primary" onclick={saveProfile}>{t('profiles.save')}</button></div>
            <div class="eq-grid">{#each eqBands as band, index}<label><span>{band}Hz</span><input type="range" min="-10" max="10" bind:value={eqValues[index]} /><b>{eqValues[index]} dB</b></label>{/each}</div>
            <div class="profile-list">{#each profiles as profile}<button onclick={() => loadProfile(profile)}><strong>{profile.name}</strong><span>{profile.preset} · {profile.axis}</span></button>{/each}<button onclick={exportProfiles}>{t('profiles.exportJson')}</button></div>
          </article>
        {/if}
      {/if}

      {#if activeTab === 'Bluetooth'}
        {#if isMouseDevice(activeDevice())}
          <article class="panel"><div class="panel-title"><h2>Bluetooth controls</h2><span>{t('common.notApplicable')}</span></div><p class="muted">{t('bt.mouseNote')}</p></article>
        {:else}
          <article class="panel">
            <div class="panel-title"><h2>Bluetooth controls</h2><span>{t('bt.subtitle')}</span></div>
            <div class="row four">
              <label>{t('bt.soundQualityPriority')}<select bind:value={btQuality} disabled={onH9II}><option value={0}>Standard</option><option value={1}>Sound quality</option><option value={2}>Stable connection</option></select></label>
              <label>BT startup<select bind:value={btStartup}><option value={0}>Off</option>{#if onH9II}<option value={1}>On</option><option value={2}>Last state</option>{:else}<option value={1}>Last state</option><option value={2}>On</option>{/if}</select></label>
              <label>Auto power off<select bind:value={autoPowerOff}>{#each onH9II ? autoPowerH9II : autoPower as minutes}<option value={minutes}>{minutes === 0 ? 'Never' : `${minutes} min`}</option>{/each}</select></label>
              <label>Connection mode<select bind:value={connectionMode} disabled={onH9II}><option value={0}>PC / USB</option><option value={1}>Bluetooth</option><option value={2}>Auto</option><option value={3}>Dual</option></select></label>
            </div>
            <div class="button-row"><button disabled={onH9II} onclick={() => applyControl('BT sound quality', () => Service.SetBTSoundQuality(selectedIndex, btQuality))}>{t('bt.applyQuality')}</button><button onclick={() => applyControl('BT startup', () => Service.SetBTStartupMode(selectedIndex, btStartup))}>{t('bt.applyStartup')}</button><button onclick={() => applyControl('Auto power off', () => Service.SetAutoPowerOff(selectedIndex, autoPowerOff))}>{t('bt.applyAutoOff')}</button><button disabled={onH9II} onclick={() => applyControl('Connection mode', () => Service.SetConnectionDestinationMode(selectedIndex, connectionMode))}>{t('bt.applyConnection')}</button></div>
            {#if onH9II}<p class="muted">{t('h9ii.unavailable')}</p>{/if}
          </article>
        {/if}
      {/if}

      {#if activeTab === 'Buttons'}
        {#if isMouseDevice(activeDevice())}
          <article class="panel">
            <div class="panel-title"><h2>Mouse-A buttons</h2><span>5-button remap</span></div>
            <div class="profile-list">
              {#each deviceState?.mouse?.buttons ?? [] as button}
                <button onclick={() => syncMouseButtonEditor(button)}><strong>{mouseButtonName(button.buttonIndex)}</strong><span>{mouseTypeDefText(button.typeDef)} · {mouseKeyText(button)}</span></button>
              {:else}
                <p class="muted">{t('buttons.noneRead')}</p>
              {/each}
            </div>
            <div class="row four">
              <label>Button<select bind:value={mouseButtonIndex}>{#each mouseButtons as button}<option value={button[0]}>{button[1]}</option>{/each}</select></label>
              <label>Preset<select bind:value={mouseButtonPreset} onchange={() => applyMousePreset(mouseButtonPreset)}>{#each mousePresets as preset}<option value={preset.label}>{preset.label}</option>{/each}<option value="Custom">Custom</option></select></label>
              <label>Type<select bind:value={mouseButtonTypeDef}>{#each mouseTypeDefs as item}<option value={item[0]}>{item[1]}</option>{/each}</select></label>
              <label>Key type<select bind:value={mouseButtonKeyType}>{#each mouseKeyTypes as item}<option value={item[0]}>{item[1]}</option>{/each}</select></label>
            </div>
            <div class="row three"><label>Key code 1<input type="number" min="0" max="255" bind:value={mouseButtonKeyCode1} /></label><label>Key code 2 / modifier<input type="number" min="0" max="255" bind:value={mouseButtonKeyCode2} /></label><button class="primary" onclick={applyMouseButton}>{t('buttons.apply')}</button></div>
            <p class="muted">{t('buttons.mouseNote')}</p>
          </article>
        {:else}
          <article class="panel">
            <div class="panel-title"><h2>Touch / button assignment</h2><span>{t('buttons.assignSubtitle')}</span></div>
            <div class="row three"><label>{t('buttons.slot')}<input type="number" min="0" max="15" bind:value={assignSlot} disabled={onH9II} /></label><label>{t('buttons.function')}<select bind:value={assignAction} disabled={onH9II}>{#each touchActions as action}<option value={action[1]}>{action[0]}</option>{/each}</select></label><button class="primary" disabled={onH9II} onclick={() => applyControl('Assignable action', () => Service.SetAssignableAction(selectedIndex, assignSlot, assignAction))}>{t('buttons.assign')}</button></div>
            {#if onH9II}<p class="muted">{t('h9ii.unavailable')}</p>{/if}
          </article>
        {/if}
      {/if}

      {#if activeTab === 'Spatial'}
        {#if isMouseDevice(activeDevice())}
          <article class="panel"><div class="panel-title"><h2>HRTF / 360 Spatial</h2><span>{t('common.notApplicable')}</span></div><p class="muted">{t('spatial.mouseNote')}</p></article>
        {:else}
          <article class="panel spatial">
            <img src="/inzone-assets/raw/resources/surround_010_on.png" alt="" />
            <div><h2>HRTF / 360 Spatial</h2><p>360 Spatial / Surround</p><label class="check"><input type="checkbox" bind:checked={surroundEnabled} disabled={onH9II} /> Surround enabled</label><button class="primary" disabled={onH9II} onclick={applySpatial}>{t('spatial.apply')}</button>{#if onH9II}<p class="muted">{t('h9ii.unavailable')}</p>{/if}</div>
          </article>
        {/if}
      {/if}
    </section>
  </div>
</main>
