import { createContext, useContext } from "react";
import type { Tone } from "./status";

export type PushToast = (tone: Tone, text: string) => void;

export const ToastContext = createContext<PushToast>(() => {});

export const useToast = (): PushToast => useContext(ToastContext);
