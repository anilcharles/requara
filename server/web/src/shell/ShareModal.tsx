import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { App, Avatar, Button, Input, Modal, Select, Space, Tag, theme, Typography } from "antd";
import { useState } from "react";
import { del, get, patch, post, type Member } from "../api";
import { initials } from "./Header";
import { useMe } from "./session";

const palette = [
  ["#E6F4FF", "#0958D9"],
  ["#F9F0FF", "#531DAB"],
  ["#FFF7E6", "#D46B08"],
  ["#F6FFED", "#389E0D"],
];

export default function ShareModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { token } = theme.useToken();
  const { message } = App.useApp();
  const qc = useQueryClient();
  const me = useMe();
  const members = useQuery({ queryKey: ["members"], queryFn: () => get<Member[]>("/api/members"), enabled: open });
  const [email, setEmail] = useState("");
  const [role, setRole] = useState("editor");
  const invite = useMutation({
    mutationFn: () => post("/api/members/invite", { email, role }),
    onSuccess: () => {
      message.success(`Invite sent to ${email}`);
      setEmail("");
      qc.invalidateQueries({ queryKey: ["members"] });
    },
    onError: (e: Error) => message.error(e.message),
  });
  const isOwner = me.data?.memberRole === "owner";
  const refresh = () => qc.invalidateQueries({ queryKey: ["members"] });
  const change = useMutation({
    mutationFn: ({ m, role }: { m: Member; role: string }) =>
      role === "remove"
        ? m.pending
          ? del(`/api/members/invite?email=${encodeURIComponent(m.email)}`)
          : del(`/api/members/${encodeURIComponent(m.userId)}`)
        : m.pending
          ? post("/api/members/invite", { email: m.email, role })
          : patch(`/api/members/${encodeURIComponent(m.userId)}`, { role }),
    onSuccess: refresh,
    onError: (e: Error) => message.error(e.message),
  });
  const workspaces = useQuery({ queryKey: ["workspaces"], queryFn: () => get<{ id: string; name: string; role: string }[]>("/api/workspaces") });
  const wsName = workspaces.data?.find((w) => w.id === me.data?.workspaceId)?.name;

  return (
    <Modal
      open={open}
      onCancel={onClose}
      title={wsName ? `Share ${wsName}` : "Share workspace"}
      width={520}
      footer={[
        <Button
          key="copy"
          onClick={() => {
            navigator.clipboard.writeText(location.origin + location.pathname);
            message.success("Link copied");
          }}
        >
          Copy link
        </Button>,
        <Button key="done" type="primary" onClick={onClose}>
          Done
        </Button>,
      ]}
    >
      <Space.Compact style={{ width: "100%", marginTop: 8 }}>
        <Input
          placeholder={isOwner ? "Add people by email" : "Only workspace owners can invite"}
          disabled={!isOwner}
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          onPressEnter={() => email && invite.mutate()}
        />
        <Select value={role} onChange={setRole} disabled={!isOwner} style={{ width: 120 }} options={[{ value: "owner", label: "Owner" }, { value: "editor", label: "Can edit" }, { value: "viewer", label: "Can view" }]} />
        <Button type="primary" disabled={!isOwner || !email} loading={invite.isPending} onClick={() => invite.mutate()}>
          Invite
        </Button>
      </Space.Compact>
      <Typography.Text type="secondary" style={{ display: "block", margin: "20px 0 12px", fontSize: 13, fontWeight: 500 }}>
        People with access
      </Typography.Text>
      <Space direction="vertical" size={12} style={{ width: "100%" }}>
        {(members.data ?? []).map((m, i) => {
          const [bg, fg] = palette[i % palette.length];
          return (
            <div key={m.email} style={{ display: "flex", alignItems: "center", gap: 12 }}>
              <Avatar style={{ background: bg, color: fg, fontSize: 12, fontWeight: 500 }}>{initials(m.name || m.email)}</Avatar>
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ fontWeight: 500 }}>
                  {m.name || m.email}
                  {m.email === me.data?.email && <span style={{ color: token.colorTextTertiary, fontWeight: 400 }}> (you)</span>}
                </div>
                <div style={{ fontSize: 12, color: token.colorTextTertiary }}>{m.email}</div>
              </div>
              {m.pending && <Tag>Invited</Tag>}
              {isOwner && m.email !== me.data?.email ? (
                <Select
                  size="small"
                  variant="borderless"
                  value={m.role}
                  style={{ width: 120 }}
                  onChange={(role) => change.mutate({ m, role })}
                  options={[
                    { value: "owner", label: "Owner" },
                    { value: "editor", label: "Can edit" },
                    { value: "viewer", label: "Can view" },
                    { value: "remove", label: <span style={{ color: token.colorError }}>{m.pending ? "Cancel invite" : "Remove"}</span> },
                  ]}
                />
              ) : (
                <Typography.Text type="secondary">{m.role === "owner" ? "Owner" : m.role === "editor" ? "Can edit" : "Can view"}</Typography.Text>
              )}
            </div>
          );
        })}
      </Space>
    </Modal>
  );
}
