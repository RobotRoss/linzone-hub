import type { MessageKey } from './en';

// Traditional Chinese (Taiwan). Strings are carried over verbatim from the
// original hard-coded UI so existing zh-TW users see no change.
export const zhTW: Record<MessageKey, string> = {
  'lang.label': '語言',

  'msg.ready': 'Ready',
  'msg.readingState': '讀取裝置狀態...',
  'msg.noDevices': '未偵測到支援的 INZONE 裝置。請確認 USB/HID 權限與 udev rule。',
  'msg.loaded': '已讀取 {model}',
  'msg.sent': '{label} 已送出',
  'msg.repairWaiting': '等待系統授權以修復 udev 權限...',
  'msg.repairDone': 'udev 權限已修復，請重新插拔裝置或重新掃描。',
  'msg.profileSaved': '已儲存 profile：{name}',
  'msg.profileLoaded': '已載入 profile：{name}',

  'ctl.headphoneVolume': '耳機音量',
  'ctl.micVolume': '麥克風音量',

  'action.rescan': '重新掃描',
  'action.refreshState': '刷新狀態',
  'warn.hidrawPermission': '偵測到 hidraw 權限不足，部分控制功能無法開啟裝置。',
  'action.fixUdev': '修復 udev 權限',

  'sidebar.devices': '裝置',
  'sidebar.empty': '沒有耳機/耳塞。',
  'device.connected': '已連線',

  'mouse.controlsTitle': 'Mouse-A 控制',
  'action.writeDeviceProfile': '寫入裝置 Profile',
  'action.restoreDefaults': '恢復預設',
  'audio.title': '音訊控制',
  'audio.liveApply': '即時套用',
  'ambient.subtitle': '環境音控制',
  'ambient.mode': '模式',

  'profiles.mouseSubtitle': '裝置內設定',
  'profiles.writeCurrent': '寫入目前設定',
  'profiles.mouseNote': 'Save to profile 會把目前 DPI、Polling rate、LOD、LED、Motion Sync、Angle snapping 與按鍵配置寫進目前的滑鼠 profile。Profile 名稱封包文件不完整，未在 GUI 中猜測實作。',
  'profiles.subtitle': '設定檔',
  'profiles.exportJson': '匯出 JSON',
  'profiles.save': '儲存',

  'common.notApplicable': '不適用',

  'bt.mouseNote': 'Mouse-A 目前沒有 Bluetooth/音訊連線控制，請使用 Live、Profiles、Buttons 分頁。',
  'bt.subtitle': '連線設定',
  'bt.soundQualityPriority': '音質優先',
  'bt.applyQuality': '套用音質',
  'bt.applyStartup': '套用啟動',
  'bt.applyAutoOff': '套用自動關機',
  'bt.applyConnection': '套用連線',

  'buttons.noneRead': '尚未讀到按鍵配置，請刷新狀態。',
  'buttons.apply': '套用按鍵',
  'buttons.mouseNote': 'Keyboard remap 可用 Key type=Keyboard、Key code 1=HID keycode、Key code 2=modifier bitmask。Modifier: LCtrl=1, LShift=2, LAlt=4, LWin=8, RCtrl=16, RShift=32, RAlt=64, RWin=128。',
  'buttons.assignSubtitle': '按鍵功能',
  'buttons.slot': '按鍵',
  'buttons.function': '功能',
  'buttons.assign': '指派',

  'spatial.mouseNote': 'Mouse-A 不支援耳機空間音訊控制。',
  'spatial.apply': '套用 Surround',
};
