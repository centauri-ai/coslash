import { useState, type ComponentType, type ReactNode, type PointerEvent as ReactPointerEvent } from 'react';
import { ChevronDownIcon, ChevronUpIcon, FocusIcon, LockIcon, PencilIcon, UnlockIcon } from 'lucide-react';
import { cn } from '@/lib/utils';
import { CANVAS_COLLAPSED_HEIGHT, type CanvasNodeBox } from '@/plugins/canvas/shared/geometry';
import { nodeCommandFor } from '@/plugins/canvas/shared/keyboard';

// Inline-editable node title. Rendered in place of the static title span when the
// node opts in with `onRename`; the pencil is revealed on header hover, Enter/blur
// commits, Escape reverts. Pointer events are stopped so editing never starts a drag.
function EditableNodeTitle({ title, onRename }: { title: string; onRename: (title: string) => void }) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(title);

  const commit = () => {
    const next = draft.trim();
    if (next && next !== title) onRename(next);
    setEditing(false);
  };

  if (editing) {
    return (
      <input
        value={draft}
        autoFocus
        onChange={(event) => setDraft(event.target.value)}
        onClick={(event) => event.stopPropagation()}
        onPointerDown={(event) => event.stopPropagation()}
        onBlur={commit}
        onKeyDown={(event) => {
          if (event.key === 'Enter') commit();
          if (event.key === 'Escape') {
            setDraft(title);
            setEditing(false);
          }
        }}
        className="focus:border-brand min-w-0 flex-1 rounded border bg-transparent px-1 text-[11px] font-extrabold tracking-widest uppercase outline-none"
        aria-label="Rename node"
      />
    );
  }

  return (
    <>
      <span className="truncate text-[11px] font-extrabold tracking-widest uppercase">{title}</span>
      <button
        type="button"
        onClick={(event) => {
          event.stopPropagation();
          setDraft(title);
          setEditing(true);
        }}
        className="text-muted-foreground hover:text-foreground shrink-0 opacity-0 group-hover:opacity-100 focus-visible:opacity-100"
        aria-label="Rename node"
        title="Rename"
      >
        <PencilIcon className="size-3" />
      </button>
    </>
  );
}

// Generic draggable/resizable/collapsible/lockable node chrome shared by all
// three Canvas products. The body is supplied by the caller; this component owns
// the header, the hover actions, and the focus/collapse affordances. `id` is
// opaque — it drives a `canvas-node-<id>` class and the interaction callbacks,
// nothing more, so no product state leaks into the shared layer.
export function CanvasNode<Id extends string>({
  id,
  title,
  icon: Icon,
  meta,
  layout,
  selected,
  focused,
  focusActive,
  className,
  children,
  onSelect,
  onFocus,
  onToggleCollapse,
  onToggleLock,
  onDragStart,
  onResizeStart,
  onRename,
}: {
  id: Id;
  title: string;
  icon: ComponentType<{ className?: string }>;
  meta?: ReactNode;
  layout: CanvasNodeBox;
  selected: boolean;
  focused: boolean;
  focusActive: boolean;
  className?: string;
  children: ReactNode;
  onSelect: (id: Id) => void;
  onFocus: (id: Id) => void;
  onToggleCollapse: (id: Id) => void;
  onToggleLock: (id: Id) => void;
  onDragStart: (event: ReactPointerEvent, id: Id) => void;
  onResizeStart: (event: ReactPointerEvent, id: Id) => void;
  // Opt in to an inline-editable title. When set, the header shows a pencil and
  // commits the new name back through this callback.
  onRename?: (id: Id, title: string) => void;
}) {
  // A focused node is positioned by the stylesheet, not by its stored box.
  const nodeStyle = focused
    ? undefined
    : {
        left: layout.x,
        top: layout.y,
        width: layout.width,
        height: layout.collapsed ? CANVAS_COLLAPSED_HEIGHT : layout.height,
      };
  return (
    <div
      className={cn('canvas-node', `canvas-node-${id}`, className, {
        'canvas-node-selected': selected,
        'canvas-node-focused': focused,
        'canvas-node-muted': focusActive && !focused,
        'canvas-node-collapsed': layout.collapsed && !focused,
      })}
      style={nodeStyle}
      role="group"
      tabIndex={0}
      aria-label={`${title.toLowerCase()} Canvas component`}
      onClick={() => onSelect(id)}
      onKeyDown={(event) => {
        // Only the chrome itself answers these keys; a button or field inside
        // the body keeps its own behavior.
        if (event.currentTarget !== event.target) return;
        const command = nodeCommandFor(event);
        if (command === null) return;
        event.preventDefault();
        if (command === 'activate') onSelect(id);
        if (command === 'toggle-collapse') onToggleCollapse(id);
        if (command === 'toggle-lock') onToggleLock(id);
      }}
      onDoubleClick={(event) => {
        if (!(event.target as HTMLElement).closest('button, input, textarea, a')) onFocus(id);
      }}
    >
      <div
        className={cn('canvas-node-header', { 'canvas-node-header-draggable': !layout.locked })}
        onPointerDown={(event) => {
          if (!(event.target as HTMLElement).closest('button, input')) onDragStart(event, id);
        }}
      >
        <div className="group flex min-w-0 items-center gap-2">
          <Icon className="text-muted-foreground size-3.5 shrink-0" />
          {onRename ? (
            <EditableNodeTitle title={title} onRename={(next) => onRename(id, next)} />
          ) : (
            <span className="truncate text-[11px] font-extrabold tracking-widest uppercase">{title}</span>
          )}
        </div>
        <div className="flex min-w-0 items-center gap-1">
          <span className="canvas-node-meta">{meta}</span>
          <span className="canvas-node-actions">
            <button
              type="button"
              onClick={(event) => {
                event.stopPropagation();
                onFocus(id);
              }}
              aria-label={focused ? 'Exit focus mode' : `Focus ${title.toLowerCase()}`}
              title={focused ? 'Exit focus mode' : 'Focus node'}
            >
              <FocusIcon />
            </button>
            <button
              type="button"
              onClick={(event) => {
                event.stopPropagation();
                onToggleLock(id);
              }}
              aria-label={layout.locked ? 'Unlock node' : 'Lock node'}
              title={layout.locked ? 'Unlock node' : 'Lock node'}
            >
              {layout.locked ? <LockIcon /> : <UnlockIcon />}
            </button>
            <button
              type="button"
              onClick={(event) => {
                event.stopPropagation();
                onToggleCollapse(id);
              }}
              aria-label={layout.collapsed ? 'Expand node' : 'Collapse node'}
              title={layout.collapsed ? 'Expand node' : 'Collapse node'}
            >
              {layout.collapsed ? <ChevronDownIcon /> : <ChevronUpIcon />}
            </button>
          </span>
        </div>
      </div>
      {(!layout.collapsed || focused) && <div className="canvas-node-body">{children}</div>}
      {!layout.locked && !layout.collapsed && !focused && (
        <button
          type="button"
          className="canvas-node-resize"
          onPointerDown={(event) => onResizeStart(event, id)}
          aria-label={`Resize ${title.toLowerCase()}`}
        />
      )}
    </div>
  );
}
