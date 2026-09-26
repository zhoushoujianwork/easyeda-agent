/** Browser-owned clock. Bare Worker/Blob/URL names are masked by the EDA sandbox. */
export interface WatchdogClock { stop(): void }

export function startWatchdogClock(tick: () => void, report: (message: string) => void): WatchdogClock {
 const host = globalThis;
 let stopped = false, degraded = false;
 let worker: Worker | undefined;
 let url: string | undefined;
 let interval: ReturnType<typeof setInterval> | undefined;
 const pulse = () => { if (!stopped) tick(); };
 const releaseWorker = () => {
  if (worker) {
   try { worker.onmessage = null; } catch { /* still release the worker */ }
   try { worker.onerror = null; } catch { /* still release the worker */ }
   try { worker.terminate(); } catch { /* still revoke the URL */ }
   worker = undefined;
  }
  if (url !== undefined) {
   try { host.URL.revokeObjectURL(url); } catch { /* best-effort after host teardown */ }
   url = undefined;
  }
 };
 const fallback = (reason: string) => {
  if (stopped || degraded) return;
  degraded = true;
  releaseWorker();
  report(`watchdog: host worker unavailable (${reason}) — main-thread interval (throttled when backgrounded)`);
  try { interval = host.setInterval(pulse, 3000); }
  catch (error) { report(`watchdog: fallback clock unavailable: ${String(error)}`); }
 };
 let stage = 'create blob URL';
 try {
  // Fixed transport clock only: no payload, EDA calls or design-object access.
  url = host.URL.createObjectURL(new host.Blob(['setInterval(function(){postMessage(0);}, 3000);'], { type: 'application/javascript' }));
  stage = 'construct worker';
  worker = new host.Worker(url);
  stage = 'attach callbacks';
  worker.onmessage = () => { if (!degraded) pulse(); };
  worker.onerror = event => fallback(event.message || 'worker error');
  if (!degraded) report('watchdog: host worker ticker started');
 }
 catch (error) { fallback(`${stage}: ${String(error)}`); }
 return {
  stop() {
   if (stopped) return;
   stopped = true;
   releaseWorker();
   if (interval !== undefined) { host.clearInterval(interval); interval = undefined; }
  },
 };
}
