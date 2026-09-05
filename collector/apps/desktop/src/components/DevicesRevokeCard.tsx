import React, { useState } from "react";
import type { CollectorDevice } from "../tauri-bridge.ts";

interface DevicesRevokeCardProps {
  devices: CollectorDevice[];
  onRevokeDevice: (deviceId: string) => Promise<void>;
  lang: "zh" | "en";
}

export const DevicesRevokeCard: React.FC<DevicesRevokeCardProps> = ({
  devices,
  onRevokeDevice,
  lang,
}) => {
  const isZh = lang === "zh";
  const [confirmDeviceId, setConfirmDeviceId] = useState<string | null>(null);

  const handleRevoke = async (id: string) => {
    await onRevokeDevice(id);
    setConfirmDeviceId(null);
  };

  return (
    <div className="card-section">
      <div className="section-head">
        <div>
          <h2>{isZh ? "已连接设备与 Ed25519 密钥撤销" : "Connected Devices & Key Revocation"}</h2>
          <p>
            {isZh
              ? "管理已通过非对称密钥绑定的终端采集器；支持随时作废指定设备的上传许可与访问凭证"
              : "Manage hardware collector instances. Revoke Ed25519 keypairs anytime to block future batch ingestions"}
          </p>
        </div>
        <span className="tag tag-lime">
          {devices.filter((d) => d.status === "ACTIVE").length} {isZh ? "台有效在线" : "Active Devices"}
        </span>
      </div>

      <div className="devices-list">
        {devices.map((dev) => (
          <div key={dev.id} className={`device-item ${dev.status === "REVOKED" ? "device-revoked" : ""}`}>
            <div className="device-icon">
              {dev.platform === "windows" ? "🪟" : "🍎"}
            </div>
            <div className="device-info">
              <div className="device-title-row">
                <span className="device-name">{dev.name}</span>
                <span
                  className={`status-pill ${
                    dev.status === "ACTIVE" ? "pill-active" : "pill-danger"
                  }`}
                >
                  {dev.status}
                </span>
              </div>
              <div className="device-details">
                <span>{dev.osVersion}</span>
                <span>•</span>
                <span>Installation ID: <code className="font-mono">{dev.installationId}</code></span>
              </div>
              <div className="device-fingerprint">
                <span className="fp-label">{isZh ? "Ed25519 指纹:" : "Key Fingerprint:"}</span>
                <code className="font-mono">{dev.keyFingerprint}</code>
              </div>
            </div>

            <div className="device-action">
              {dev.status === "ACTIVE" ? (
                confirmDeviceId === dev.id ? (
                  <div className="confirm-row">
                    <span className="confirm-tip">{isZh ? "确认撤销?" : "Confirm?"}</span>
                    <button
                      type="button"
                      className="btn btn-danger-sm"
                      onClick={() => handleRevoke(dev.id)}
                    >
                      {isZh ? "确认作废" : "Revoke"}
                    </button>
                    <button
                      type="button"
                      className="btn btn-muted-sm"
                      onClick={() => setConfirmDeviceId(null)}
                    >
                      {isZh ? "取消" : "Cancel"}
                    </button>
                  </div>
                ) : (
                  <button
                    type="button"
                    className="btn btn-outline-danger"
                    onClick={() => setConfirmDeviceId(dev.id)}
                  >
                    {isZh ? "撤销此设备" : "Revoke Device"}
                  </button>
                )
              ) : (
                <span className="revoked-badge">
                  {dev.status === "REVOCATION_PENDING"
                    ? (isZh ? "等待服务端确认" : "Pending confirmation")
                    : (isZh ? "密钥已注销" : "Revoked")}
                </span>
              )}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
};
