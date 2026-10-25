import { useCallback, useEffect, useRef, useState } from "react";
import { toApiError } from "../api";
import type { ApiError } from "../api";

export type RequestState<T> =
  | { status: "idle" }
  | { status: "loading" }
  | { status: "success"; data: T }
  | { status: "error"; error: ApiError };

/**
 * Runs `load` whenever `key` changes (and on `reload()`); `null` means "not
 * asked yet" (idle). Unlike ui-kit's useFetch this keeps the RFC 9457 body of
 * the failure (title, detail, slug) so a screen can explain exactly what went
 * wrong.
 */
export function useRequest<T>(
  load: (() => Promise<T>) | null,
  key: string,
): RequestState<T> & { reload: () => void } {
  const loadRef = useRef(load);
  useEffect(() => {
    loadRef.current = load;
  });
  const active = load !== null;
  const [nonce, setNonce] = useState(0);
  const [state, setState] = useState<RequestState<T>>(active ? { status: "loading" } : { status: "idle" });

  useEffect(() => {
    const fn = loadRef.current;
    if (!fn) {
      setState({ status: "idle" });
      return;
    }
    let stale = false;
    setState({ status: "loading" });
    fn().then(
      (data) => {
        if (!stale) setState({ status: "success", data });
      },
      (err: unknown) => {
        if (!stale) setState({ status: "error", error: toApiError(err) });
      },
    );
    return () => {
      stale = true;
    };
  }, [key, nonce, active]);

  const reload = useCallback(() => setNonce((n) => n + 1), []);
  return { ...state, reload };
}
