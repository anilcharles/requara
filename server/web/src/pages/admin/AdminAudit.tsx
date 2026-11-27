import { DownloadOutlined } from "../../ui/icons";
import { useInfiniteQuery } from "@tanstack/react-query";
import { Button, DatePicker, Input, Select, Space, Table, Tag, Tooltip, Typography } from "../../ui/kit";
import dayjs, { type Dayjs } from "dayjs";
import { useMemo, useState } from "react";
import { get, type AuditEvent } from "../../api";
import { mono, PageHeader } from "../../components/bits";

const AREAS = [
  { value: "", label: "All events" },
  { value: "auth.", label: "Sign-ins and sign-outs" },
  { value: "user.", label: "Accounts" },
  { value: "invite.", label: "Invites" },
  { value: "member.", label: "Workspace members" },
  { value: "workspace.", label: "Workspaces" },
  { value: "api_key.", label: "API keys" },
  { value: "admin_token.", label: "Admin tokens" },
  { value: "scim.", label: "SCIM provisioning" },
  { value: "settings.", label: "Settings" },
  { value: "api.", label: "API publishing" },
];

/** A readable sentence for each action. */
const VERB: Record<string, string> = {
  "auth.signin": "signed in",
  "auth.signout": "signed out",
  "auth.email_link": "requested a sign-in link",
  "user.deactivated": "deactivated",
  "user.reactivated": "reactivated",
  "user.signed_out": "signed out everywhere",
  "user.deleted": "deleted",
  "invite.created": "invited",
  "invite.revoked": "revoked the invite for",
  "member.role_set": "set the role of",
  "member.removed": "removed",
  "member.left": "left the workspace",
  "workspace.created": "created workspace",
  "workspace.updated": "updated workspace",
  "workspace.deleted": "deleted workspace",
  "api_key.created": "created API key",
  "api_key.revoked": "revoked an API key",
  "admin_token.created": "created admin token",
  "admin_token.revoked": "revoked admin token",
  "scim.user_created": "provisioned",
  "scim.user_updated": "updated directory user",
  "scim.user_deleted": "deprovisioned",
  "settings.updated": "changed settings",
  "history.purged": "deleted all request history",
  "audit.exported": "exported the audit log",
  "api.published": "published",
  "session.revoked": "signed out a session of",
};

export default function AdminAudit() {
  const [area, setArea] = useState("");
  const [outcome, setOutcome] = useState("");
  const [q, setQ] = useState("");
  const [range, setRange] = useState<[Dayjs | null, Dayjs | null] | null>(null);
  const filter = useMemo(() => {
    const p = new URLSearchParams();
    if (area) p.set("action", area);
    if (outcome) p.set("outcome", outcome);
    if (q) p.set("q", q);
    if (range?.[0]) p.set("from", range[0].startOf("day").toISOString());
    if (range?.[1]) p.set("to", range[1].endOf("day").toISOString());
    return p;
  }, [area, outcome, q, range]);
  const events = useInfiniteQuery({
    queryKey: ["admin", "audit", filter.toString()],
    initialPageParam: 0,
    queryFn: ({ pageParam }) => {
      const p = new URLSearchParams(filter);
      if (pageParam) p.set("before", String(pageParam));
      return get<{ items: AuditEvent[]; more: boolean; retentionDays: number }>(`/api/admin/audit?${p}`);
    },
    getNextPageParam: (last) => (last.more ? last.items[last.items.length - 1]?.id : undefined),
  });
  const rows = events.data?.pages.flatMap((p) => p.items) ?? [];
  const retention = events.data?.pages[0]?.retentionDays;
  return (
    <>
      <PageHeader
        title="Audit log"
        subtitle={retention ? `Kept for ${retention} days (REQUARA_AUDIT_RETENTION_DAYS)` : "Kept forever"}
        extra={
          <Button icon={<DownloadOutlined />} href={`/api/admin/audit.csv?${filter}`}>
            Export CSV
          </Button>
        }
      />
      <Space wrap style={{ marginBottom: 12 }}>
        <Select value={area} onChange={setArea} options={AREAS} style={{ width: 220 }} />
        <Select
          value={outcome}
          onChange={setOutcome}
          style={{ width: 140 }}
          options={[
            { value: "", label: "Any outcome" },
            { value: "success", label: "Succeeded" },
            { value: "failure", label: "Failed" },
          ]}
        />
        <DatePicker.RangePicker value={range} onChange={(v) => setRange(v as [Dayjs | null, Dayjs | null] | null)} />
        <Input.Search allowClear placeholder="Search people, targets, details" onSearch={setQ} style={{ width: 280 }} />
      </Space>
      <Table
        size="small"
        rowKey="id"
        loading={events.isLoading}
        dataSource={rows}
        pagination={false}
        expandable={{
          expandedRowRender: (e) => (
            <div style={{ ...mono, fontSize: 12, whiteSpace: "pre-wrap" }}>
              {JSON.stringify({ actor: { kind: e.actorKind, id: e.actorId }, target: { type: e.targetType, id: e.targetId }, workspaceId: e.workspaceId, ip: e.ip, userAgent: e.userAgent, ...e.details }, null, 2)}
            </div>
          ),
        }}
        columns={[
          {
            title: "Time",
            dataIndex: "at",
            width: 170,
            render: (v: string) => (
              <Tooltip title={dayjs(v).format("YYYY-MM-DD HH:mm:ss Z")}>
                <span>{dayjs(v).format("MMM D, HH:mm:ss")}</span>
              </Tooltip>
            ),
          },
          {
            title: "Event",
            render: (_, e) => (
              <span>
                <strong>{e.actorLabel || (e.actorKind === "anonymous" ? "Someone" : e.actorKind)}</strong>
                {e.actorKind === "token" && <Tag style={{ marginLeft: 6 }}>token</Tag>} {VERB[e.action] ?? e.action} {e.targetLabel && <strong>{e.targetLabel}</strong>}
                {typeof e.details.role === "string" && <span> as {e.details.role}</span>}
                {typeof e.details.provider === "string" && <Typography.Text type="secondary"> · {e.details.provider}</Typography.Text>}
                {typeof e.details.reason === "string" && <Typography.Text type="danger"> · {e.details.reason}</Typography.Text>}
              </span>
            ),
          },
          { title: "Action", dataIndex: "action", width: 170, render: (v: string) => <span style={{ ...mono, fontSize: 12 }}>{v}</span> },
          { title: "", dataIndex: "outcome", width: 90, render: (v: string) => (v === "failure" ? <Tag color="error">Failed</Tag> : null) },
          { title: "IP", dataIndex: "ip", width: 140, render: (v: string) => <span style={{ fontSize: 12 }}>{v}</span> },
        ]}
      />
      {events.hasNextPage && (
        <div style={{ textAlign: "center", marginTop: 16 }}>
          <Button loading={events.isFetchingNextPage} onClick={() => events.fetchNextPage()}>
            Load older events
          </Button>
        </div>
      )}
    </>
  );
}
