export function syncStatusText(status: string | undefined, pending: number, zh: boolean): string {
  const t = (cn: string, en: string) => zh ? cn : en;
  switch (status) {
    case "SYNCING": return t("同步中", "Syncing");
    case "SYNCED": return pending > 0 ? t("等待自动同步", "Sync scheduled") : t("已同步", "Synced");
    case "WAITING": return t("等待自动同步", "Sync scheduled");
    case "RETRYING": return t("同步未完成，稍后自动重试", "Sync incomplete · Retrying");
    case "DATA_REJECTED": return t("部分记录校验未通过，已保留在本机", "Some records rejected · Kept locally");
    case "PAUSED": return t("同步已暂停", "Sync paused");
    case "NEEDS_PROFILE": return t("完善网站资料后同步", "Complete your web profile to sync");
    case "NEEDS_ATTENTION": return t("同步受阻，请检查网站设备状态", "Sync blocked · Check device on web");
    case "CLIENT_UPGRADE_REQUIRED": return t("更新后恢复同步，本机采集继续", "Update to resume sync · Local collection continues");
    case "DEVICE_BOUND_ELSEWHERE": return t("此设备已绑定其他账号，请先在原账号解绑", "Unbind this device from its previous account first");
    default: return t("登录后自动同步", "Sign in to sync automatically");
  }
}
