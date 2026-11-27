import { DeleteOutlined } from "../ui/icons";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Button, Empty, Modal, Popconfirm, Table, Typography } from "../ui/kit";
import dayjs from "dayjs";
import { del, get, personalActive, type Cookie } from "../api";
import { mono } from "../components/bits";

/** Cookies the request runner received; they're sent back to matching URLs. */
export default function CookiesModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const qc = useQueryClient();
  const cookies = useQuery({ queryKey: ["cookies"], queryFn: () => get<Cookie[]>("/api/cookies"), enabled: open });
  const remove = useMutation({
    mutationFn: (q: Partial<Cookie>) => del(`/api/cookies?${new URLSearchParams(q as Record<string, string>)}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["cookies"] }),
  });
  return (
    <Modal title="Cookies" open={open} onCancel={onClose} footer={null} width={820}>
      <Typography.Paragraph type="secondary">
        Cookies that responses set are kept here and sent with later requests to the same site, like a browser.
        {personalActive() && " In My workspace this needs the local agent: browsers don't let web pages send cookie headers."}
      </Typography.Paragraph>
      <Table
        size="small"
        rowKey={(c) => `${c.domain}|${c.path}|${c.name}`}
        loading={cookies.isLoading}
        dataSource={cookies.data ?? []}
        pagination={false}
        locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="No cookies yet" /> }}
        columns={[
          { title: "Domain", dataIndex: "domain" },
          { title: "Name", dataIndex: "name", render: (v: string) => <span style={mono}>{v}</span> },
          { title: "Value", dataIndex: "value", ellipsis: true, render: (v: string) => <span style={{ ...mono, fontSize: 12 }}>{v}</span> },
          { title: "Path", dataIndex: "path", width: 80 },
          { title: "Expires", dataIndex: "expires", width: 140, render: (v?: string) => (v ? dayjs(v).format("MMM D, HH:mm") : "Session") },
          {
            title: "",
            width: 48,
            render: (_, c) => <Button size="small" type="text" icon={<DeleteOutlined />} aria-label="Delete cookie" onClick={() => remove.mutate({ domain: c.domain, path: c.path, name: c.name })} />,
          },
        ]}
      />
      {!!cookies.data?.length && (
        <Popconfirm title="Delete all cookies?" onConfirm={() => remove.mutate({})}>
          <Button danger style={{ marginTop: 12 }}>
            Delete all
          </Button>
        </Popconfirm>
      )}
    </Modal>
  );
}
