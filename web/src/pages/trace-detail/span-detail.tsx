import { useRef, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { ScrollArea } from "@/components/ui/scroll-area";
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetDescription,
} from "@/components/ui/sheet";
import { TraceSpan, kindColors, durationMs, formatDuration, StatusIcon } from "@/lib/trace-utils";
import { Clock, Tag, Zap, Coins, XCircle } from "lucide-react";

interface SpanDetailProps {
  span: TraceSpan | null;
  open: boolean;
  onClose: () => void;
}

function MediaPreview({ mediaKey, attrs }: { mediaKey: string; attrs: Record<string, any> }) {
  const [error, setError] = useState(false);
  const src = `/api/v1/media/${mediaKey}`;
  const replyType = String(attrs["reply.type"] || "");
  const isImage = replyType === "image" || /\.(jpg|jpeg|png|gif|webp)$/i.test(mediaKey);

  if (error) {
    return (
      <a
        href={src}
        target="_blank"
        rel="noopener noreferrer"
        className="text-primary hover:underline"
      >
        {mediaKey}
      </a>
    );
  }

  if (isImage) {
    return (
      <img
        src={src}
        alt={mediaKey}
        className="mt-1 max-h-[240px] max-w-full rounded-md border sm:max-w-[240px]"
        loading="lazy"
        onError={() => setError(true)}
      />
    );
  }

  return (
    <a
      href={src}
      target="_blank"
      rel="noopener noreferrer"
      className="text-primary hover:underline"
    >
      {mediaKey}
    </a>
  );
}

function Section({
  title,
  icon,
  children,
}: {
  title: string;
  icon: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-2">
      <div className="flex items-center gap-1.5 text-xs font-semibold text-muted-foreground uppercase tracking-wider">
        {icon}
        {title}
      </div>
      {children}
    </div>
  );
}

export function SpanDetail({ span, open, onClose }: SpanDetailProps) {
  // Keep last span so the Sheet close animation can still render content
  const lastSpanRef = useRef<TraceSpan | null>(null);
  if (span) lastSpanRef.current = span;
  const displaySpan = span ?? lastSpanRef.current;
  if (!displaySpan) return null;

  const dur = durationMs(displaySpan);
  const attrs = displaySpan.attributes ? Object.entries(displaySpan.attributes) : [];
  const events = displaySpan.events || [];

  return (
    <Sheet open={open} onOpenChange={(v) => !v && onClose()}>
      {/* ui 层 side=right 带 data-[side=right]:w-3/4（data 变体优先级高于普通 class），
          窄屏用 min-width 顶到 85vw，与 ui 层移动端抽屉上限一致，避免 320px 下内容被压扁 */}
      <SheetContent side="right" className="max-sm:min-w-[85vw] sm:w-[400px] sm:max-w-[400px] p-0">
        <SheetHeader className="p-4 pb-3 border-b">
          <div className="flex items-center gap-2 mb-1">
            <StatusIcon code={displaySpan.status_code} size="w-4 h-4" />
            <Badge
              variant="outline"
              className={`text-[9px] h-4 px-1.5 leading-none text-white ${kindColors[displaySpan.kind] || "bg-gray-400"}`}
            >
              {displaySpan.kind}
            </Badge>
            <span className="text-[10px] font-mono text-muted-foreground">
              {formatDuration(dur)}
            </span>
          </div>
          <SheetTitle className="font-mono text-sm truncate">{displaySpan.name}</SheetTitle>
          {displaySpan.status_message && (
            <SheetDescription className="text-destructive text-xs line-clamp-2">
              {displaySpan.status_message}
            </SheetDescription>
          )}
        </SheetHeader>

        <ScrollArea className="h-[calc(100vh-120px)] px-4 py-3 max-sm:h-[calc(100dvh-140px)]">
          <div className="space-y-5">
            {/* Error */}
            {displaySpan.status_message && (
              <Section title="Error" icon={<XCircle className="w-3 h-3 text-destructive" />}>
                <div className="rounded-md border border-destructive/30 bg-destructive/5 px-3 py-2 text-xs text-destructive whitespace-pre-wrap break-words font-mono">
                  {displaySpan.status_message}
                </div>
              </Section>
            )}
            {/* Timing */}
            <Section title="Timing" icon={<Clock className="w-3 h-3" />}>
              <div className="grid grid-cols-2 gap-2 text-xs">
                <div className="min-w-0 space-y-0.5">
                  <div className="text-muted-foreground">Start</div>
                  <div className="font-mono">
                    {new Date(displaySpan.start_time).toLocaleTimeString([], {
                      hour: "2-digit",
                      minute: "2-digit",
                      second: "2-digit",
                    } as Intl.DateTimeFormatOptions)}
                  </div>
                </div>
                <div className="min-w-0 space-y-0.5">
                  <div className="text-muted-foreground">End</div>
                  <div className="font-mono">
                    {displaySpan.end_time
                      ? new Date(displaySpan.end_time).toLocaleTimeString([], {
                          hour: "2-digit",
                          minute: "2-digit",
                          second: "2-digit",
                        } as Intl.DateTimeFormatOptions)
                      : "—"}
                  </div>
                </div>
                <div className="min-w-0 space-y-0.5">
                  <div className="text-muted-foreground">Duration</div>
                  <div className="font-mono font-medium">{formatDuration(dur)}</div>
                </div>
                <div className="min-w-0 space-y-0.5">
                  <div className="text-muted-foreground">Span ID</div>
                  <div className="font-mono text-[10px] truncate">{displaySpan.span_id}</div>
                </div>
              </div>
            </Section>

            {/* Token Usage */}
            {displaySpan.attributes?.["ai.tokens.total"] && (
              <Section title="Token Usage" icon={<Coins className="w-3 h-3" />}>
                {displaySpan.attributes["ai.model"] && (
                  <div className="text-xs text-muted-foreground mb-2">
                    模型:{" "}
                    <span className="font-mono font-medium text-foreground">
                      {displaySpan.attributes["ai.model"]}
                    </span>
                  </div>
                )}
                <div className="grid grid-cols-2 gap-2 text-xs sm:grid-cols-3">
                  <div className="min-w-0 space-y-0.5">
                    <div className="text-muted-foreground">Prompt</div>
                    <div className="font-mono font-medium">
                      {displaySpan.attributes["ai.tokens.prompt"] || "0"}
                    </div>
                  </div>
                  <div className="min-w-0 space-y-0.5">
                    <div className="text-muted-foreground">Completion</div>
                    <div className="font-mono font-medium">
                      {displaySpan.attributes["ai.tokens.completion"] || "0"}
                    </div>
                  </div>
                  <div className="min-w-0 space-y-0.5">
                    <div className="text-muted-foreground">Total</div>
                    <div className="font-mono font-medium">
                      {displaySpan.attributes["ai.tokens.total"]}
                    </div>
                  </div>
                  {displaySpan.attributes["ai.tokens.cached"] && (
                    <div className="min-w-0 space-y-0.5">
                      <div className="text-muted-foreground">Cached</div>
                      <div className="font-mono font-medium">
                        {displaySpan.attributes["ai.tokens.cached"]}
                      </div>
                    </div>
                  )}
                  {displaySpan.attributes["ai.tokens.reasoning"] && (
                    <div className="min-w-0 space-y-0.5">
                      <div className="text-muted-foreground">Reasoning</div>
                      <div className="font-mono font-medium">
                        {displaySpan.attributes["ai.tokens.reasoning"]}
                      </div>
                    </div>
                  )}
                </div>
              </Section>
            )}

            {/* Attributes */}
            {attrs.length > 0 && (
              <Section title="Attributes" icon={<Tag className="w-3 h-3" />}>
                <div className="rounded-md border bg-muted/30 overflow-hidden">
                  {attrs.map(([key, value], i) => (
                    <div
                      key={key}
                      className={`flex min-w-0 flex-col gap-0.5 px-3 py-1.5 text-xs sm:flex-row sm:gap-3 ${
                        i < attrs.length - 1 ? "border-b border-border/50" : ""
                      }`}
                    >
                      <span className="text-blue-500 font-semibold shrink-0 break-all sm:min-w-[100px]">
                        {key}
                      </span>
                      <span className="min-w-0 text-foreground/80 whitespace-pre-wrap break-all">
                        {key === "reply.media_key" ? (
                          <MediaPreview
                            mediaKey={String(value)}
                            attrs={Object.fromEntries(attrs)}
                          />
                        ) : (
                          String(value)
                        )}
                      </span>
                    </div>
                  ))}
                </div>
              </Section>
            )}

            {/* Events */}
            {events.length > 0 && (
              <Section title="Events" icon={<Zap className="w-3 h-3" />}>
                <div className="space-y-2">
                  {events.map((evt, i) => (
                    <div key={i} className="rounded-md border bg-muted/30 px-3 py-2">
                      <div className="flex min-w-0 items-center justify-between gap-2 mb-1">
                        <span className="min-w-0 truncate text-xs font-semibold">{evt.name}</span>
                        <span className="text-[9px] font-mono text-muted-foreground">
                          {new Date(evt.timestamp).toLocaleTimeString([], {
                            hour: "2-digit",
                            minute: "2-digit",
                            second: "2-digit",
                          })}
                        </span>
                      </div>
                      {evt.attributes && Object.keys(evt.attributes).length > 0 && (
                        <div className="space-y-0.5 mt-1">
                          {Object.entries(evt.attributes).map(([k, v]) => (
                            <div key={k} className="flex min-w-0 gap-2 text-[10px]">
                              <span className="text-blue-500 font-bold shrink-0">{k}:</span>
                              <span className="min-w-0 text-foreground/70 whitespace-pre-wrap break-all">
                                {String(v)}
                              </span>
                            </div>
                          ))}
                        </div>
                      )}
                    </div>
                  ))}
                </div>
              </Section>
            )}
          </div>
        </ScrollArea>
      </SheetContent>
    </Sheet>
  );
}
