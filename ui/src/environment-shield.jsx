import React from "react";
import { Shield, ShieldCheck } from "lucide-react";

export function EnvironmentShield({ healthy, className = "", size = 20 }) {
  const Icon = healthy ? ShieldCheck : Shield;
  return (
    <Icon
      size={size}
      className={`environment-shield ${healthy ? "healthy" : ""} ${className}`}
      role="img"
      aria-label={healthy ? "环境运行正常，网络检查通过" : "Cage 环境"}
      strokeWidth={1.8}
    />
  );
}
