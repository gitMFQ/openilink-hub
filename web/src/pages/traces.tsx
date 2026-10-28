import { useParams, Link } from "react-router-dom";
import { ArrowLeft } from "lucide-react";
import { Button } from "@/components/ui/button";
import { BotTracesTab } from "./bot-traces-tab";

export function TracesPage() {
  const { id } = useParams<{ id: string }>();

  if (!id) return null;

  return (
    <div className="flex flex-col gap-6 min-w-0">
      <div className="flex items-center gap-2">
        <Button
          variant="ghost"
          size="sm"
          className="gap-1.5 text-muted-foreground hover:text-foreground -ml-2 max-sm:h-10"
          asChild
        >
          <Link to={`/dashboard/accounts/${id}`}>
            <ArrowLeft className="h-4 w-4" />
            返回账号
          </Link>
        </Button>
      </div>
      <div className="flex min-w-0 flex-wrap items-start justify-between gap-4">
        <div className="min-w-0">
          <h1 className="text-2xl font-bold tracking-tight max-sm:text-xl">消息追踪</h1>
          <p className="text-sm text-muted-foreground mt-0.5">查看消息处理全链路日志。</p>
        </div>
      </div>
      <BotTracesTab botId={id} />
    </div>
  );
}
