import React, { createContext, useContext } from "react";
import { isSafetyCheckMessage } from "./safety-state.mjs";

export const SafetyChecklistContext = createContext(null);

export function SafetyMessage({ message }) {
  const openChecklist = useContext(SafetyChecklistContext);
  if (!openChecklist || !isSafetyCheckMessage(message)) return message;
  return <>
    <span className="safety-message-text">{message}</span>
    <button type="button" className="text-button safety-checklist-link" onClick={openChecklist}>
      查看检查清单
    </button>
  </>;
}
