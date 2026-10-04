import { Download, X } from 'lucide-react';
import { useEffect, useState } from 'react';

import { desktop, onUpdateAvailable, type UpdateInfo } from '@/lib/wails';
import { Button } from '@/ui/button';

const DISMISSED_KEY = 'calendium.update.dismissed';

function dismissedVersion(): string {
  try {
    return localStorage.getItem(DISMISSED_KEY) ?? '';
  } catch {
    return '';
  }
}

function persistDismissed(version: string): void {
  try {
    localStorage.setItem(DISMISSED_KEY, version);
  } catch {
    // Best effort: the banner still hides for this session.
  }
}

/**
 * One-line "a newer Calendium is available" bar (update v1 = notify only; no
 * in-place updater). Mounted between the titlebar and the body in App.tsx.
 * Reads GetUpdateStatus on mount (the host may have checked before this view
 * existed) and listens for update-available afterwards. Later hides it for
 * that version only; a newer release re-shows it. No modal, no toast.
 */
export function UpdateBanner() {
  const [info, setInfo] = useState<UpdateInfo | null>(null);
  const [dismissed, setDismissed] = useState<string>(() => dismissedVersion());

  useEffect(() => {
    let active = true;
    void desktop.GetUpdateStatus().then((status) => {
      if (active && status.available) setInfo(status);
    });
    const off = onUpdateAvailable((next) => {
      if (active) setInfo(next);
    });
    return () => {
      active = false;
      off();
    };
  }, []);

  if (!info?.available || info.latest === dismissed) return null;

  return (
    <div
      role="status"
      className="flex h-9 shrink-0 items-center gap-3 border-b bg-accent/40 px-3 text-xs"
    >
      <span className="font-medium">Calendium {info.latest} is available.</span>
      <span className="text-muted-foreground">You have {info.current}.</span>
      <div className="ml-auto flex items-center gap-1">
        <Button
          variant="outline"
          size="sm"
          className="h-7"
          onClick={() => void desktop.OpenExternal(info.url)}
        >
          <Download /> Download
        </Button>
        <Button
          variant="ghost"
          size="sm"
          className="h-7"
          onClick={() => {
            persistDismissed(info.latest);
            setDismissed(info.latest);
          }}
        >
          <X /> Later
        </Button>
      </div>
    </div>
  );
}
