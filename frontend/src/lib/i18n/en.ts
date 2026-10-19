// English is the source-of-truth catalog: its keys define `MessageKey`,
// and every other locale must provide the same keys (enforced by svelte-check).
export const en = {
  'lang.label': 'Language',

  // Status messages
  'msg.ready': 'Ready',
  'msg.readingState': 'Reading device state...',
  'msg.noDevices': 'No supported INZONE device detected. Check USB/HID permissions and the udev rule.',
  'msg.loaded': 'Loaded {model}',
  'msg.sent': '{label} sent',
  'msg.repairWaiting': 'Waiting for system authorization to repair udev permissions...',
  'msg.repairDone': 'udev permissions repaired. Replug the device or rescan.',
  'msg.profileSaved': 'Saved profile: {name}',
  'msg.profileLoaded': 'Loaded profile: {name}',

  // Control labels reused in status messages
  'ctl.headphoneVolume': 'Headphone volume',
  'ctl.micVolume': 'Microphone volume',
  'ctl.mic': 'Microphone',
  'mic.muted': 'Muted',
  'mic.attached': 'Attached',
  'mic.detached': 'Detached',

  // Top bar / permission banner
  'action.rescan': 'Rescan',
  'action.refreshState': 'Refresh state',
  'warn.hidrawPermission': "Insufficient hidraw permissions; some controls can't open the device.",
  'action.fixUdev': 'Fix udev permissions',
  'warn.otherMode': 'Unable to communicate with device; is the transmitter in "PC" mode?',

  // Sidebar / hero
  'sidebar.devices': 'Devices',
  'sidebar.empty': 'No headsets or earbuds.',
  'device.connected': 'Connected',

  // Live tab
  'mouse.controlsTitle': 'Mouse-A controls',
  'action.writeDeviceProfile': 'Write to device profile',
  'action.restoreDefaults': 'Restore defaults',
  'audio.title': 'Audio controls',
  'audio.liveApply': 'Applies live',
  'ambient.subtitle': 'Ambient sound control',
  'ambient.mode': 'Mode',

  // Profiles tab
  'profiles.mouseSubtitle': 'On-device settings',
  'profiles.writeCurrent': 'Write current settings',
  'profiles.mouseNote': "Save to profile writes the current DPI, polling rate, LOD, LED, Motion Sync, angle snapping and button mapping into the mouse's current profile. The packet documentation for profile names is incomplete, so the GUI does not guess at an implementation.",
  'profiles.subtitle': 'Profiles',
  'profiles.exportJson': 'Export JSON',
  'profiles.save': 'Save',

  // Shared
  'common.notApplicable': 'Not applicable',
  'state.on': 'On',
  'state.off': 'Off',
  'state.connected': 'connected',
  'h9ii.unavailable': 'Not available on the H9 II over USB.',

  // Bluetooth tab
  'bt.mouseNote': 'Mouse-A has no Bluetooth/audio connection controls. Use the Live, Profiles and Buttons tabs.',
  'bt.subtitle': 'Connection settings',
  'bt.soundQualityPriority': 'Sound quality priority',
  'bt.applyQuality': 'Apply sound quality',
  'bt.applyStartup': 'Apply startup',
  'bt.applyAutoOff': 'Apply auto power off',
  'bt.applyConnection': 'Apply connection',

  // Buttons tab
  'buttons.noneRead': 'No button mapping read yet. Refresh the state.',
  'buttons.apply': 'Apply button',
  'buttons.mouseNote': 'For keyboard remap, set Key type=Keyboard, Key code 1=HID keycode and Key code 2=modifier bitmask. Modifiers: LCtrl=1, LShift=2, LAlt=4, LWin=8, RCtrl=16, RShift=32, RAlt=64, RWin=128.',
  'buttons.assignSubtitle': 'Button functions',
  'buttons.slot': 'Button',
  'buttons.function': 'Function',
  'buttons.assign': 'Assign',

  // Spatial tab
  'spatial.mouseNote': 'Mouse-A does not support headset spatial audio controls.',
  'spatial.apply': 'Apply Surround',
};

export type MessageKey = keyof typeof en;
