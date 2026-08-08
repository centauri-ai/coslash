import { useEffect, type ReactNode } from 'react';
import { XIcon } from 'lucide-react';
import { cn } from '@/lib/utils';

// Reusable floating shells needed by all three Canvas products: the narrow
// inspector, the wider side panel, and the command-palette overlay. They carry
// chrome and dismissal behavior only — content and state stay with the product.

function CloseButton({ onClose, label }: { onClose: () => void; label: string }) {
  return (
    <button
      type="button"
      onClick={onClose}
      className="text-muted-foreground hover:text-foreground shrink-0"
      aria-label={label}
      title={label}
    >
      <XIcon className="size-3.5" />
    </button>
  );
}

function PanelShell({
  variant,
  title,
  onClose,
  className,
  children,
}: {
  variant: 'inspector' | 'side-panel';
  title: ReactNode;
  onClose?: () => void;
  className?: string;
  children: ReactNode;
}) {
  return (
    <aside
      className={cn(`canvas-${variant}`, className)}
      aria-label={typeof title === 'string' ? title : undefined}
    >
      <div className={`canvas-${variant}-header`}>
        <span className="truncate text-[11px] font-extrabold tracking-widest uppercase">{title}</span>
        {onClose !== undefined && <CloseButton onClose={onClose} label="Close panel" />}
      </div>
      <div className={`canvas-${variant}-body`}>{children}</div>
    </aside>
  );
}

export function CanvasInspector(props: {
  title: ReactNode;
  onClose?: () => void;
  className?: string;
  children: ReactNode;
}) {
  return <PanelShell variant="inspector" {...props} />;
}

export function CanvasSidePanel(props: {
  title: ReactNode;
  onClose?: () => void;
  className?: string;
  children: ReactNode;
}) {
  return <PanelShell variant="side-panel" {...props} />;
}

/**
 * Command-palette overlay. Escape and a backdrop click both dismiss; the
 * listener is bound at the document so it works no matter where focus sits.
 */
export function CanvasCommandOverlay({
  open,
  onClose,
  className,
  children,
}: {
  open: boolean;
  onClose: () => void;
  className?: string;
  children: ReactNode;
}) {
  useEffect(() => {
    if (!open) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose();
    };
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, [open, onClose]);

  if (!open) return null;
  return (
    <div
      className="canvas-command-backdrop"
      // Only a click on the backdrop itself dismisses; clicks inside the palette bubble here too.
      onClick={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <div className={cn('canvas-command-palette', className)} role="dialog" aria-modal="true">
        {children}
      </div>
    </div>
  );
}
