import { C2OperationDetailView } from "@/components/c2-operation-detail-view";

export default async function C2OperationPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  return <C2OperationDetailView id={id} />;
}
