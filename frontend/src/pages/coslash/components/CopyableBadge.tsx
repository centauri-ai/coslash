import { useEffect, useState, type ReactNode } from 'react';
import { Badge } from '@/components/ui/badge';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { cn } from '@/lib/utils';

const CONFIRMATION_MS = 2000;

export function CopyableBadge({
  value,
  ariaLabel,
  copiedLabel,
  className,
  children,
}: {
  value: string;
  ariaLabel: string;
  copiedLabel: string;
  className?: string;
  children: ReactNode;
}) {
  const [open, setOpen] = useState(false);
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    if (!copied) return;
    const timer = setTimeout(() => setCopied(false), CONFIRMATION_MS);
    return () => clearTimeout(timer);
  }, [copied]);

  const copy = () => {
    if (navigator.clipboard == null) return;
    setCopied(true);
    void navigator.clipboard.writeText(value).catch(() => setCopied(false));
  };

  return (
    <Tooltip open={open || copied} onOpenChange={setOpen}>
      <TooltipTrigger asChild>
        <Badge
          variant="secondary"
          className={cn('min-w-0 cursor-pointer', className)}
          role="button"
          tabIndex={0}
          aria-label={copied ? copiedLabel : ariaLabel}
          onClick={(event) => {
            event.stopPropagation();
            copy();
          }}
          onKeyDown={(event) => {
            if (event.key !== 'Enter' && event.key !== ' ') return;
            event.preventDefault();
            event.stopPropagation();
            copy();
          }}
        >
          {children}
        </Badge>
      </TooltipTrigger>
      <TooltipContent className="font-mono break-all">
        {copied ? copiedLabel : `${value} · click to copy`}
      </TooltipContent>
    </Tooltip>
  );
}
