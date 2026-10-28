import { Badge } from "@/components/ui/badge";

export function ListingBadge({ listing }: { listing?: string }) {
  if (listing === "listed")
    return (
      <Badge variant="default" className="shrink-0 whitespace-nowrap">
        已上架
      </Badge>
    );
  if (listing === "pending")
    return (
      <Badge
        variant="outline"
        className="shrink-0 whitespace-nowrap text-orange-500 border-orange-500"
      >
        审核中
      </Badge>
    );
  if (listing === "rejected")
    return (
      <Badge variant="destructive" className="shrink-0 whitespace-nowrap">
        已拒绝
      </Badge>
    );
  return (
    <Badge variant="secondary" className="shrink-0 whitespace-nowrap">
      未上架
    </Badge>
  );
}
