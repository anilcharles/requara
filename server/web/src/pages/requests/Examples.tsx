import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Button, Dropdown, Modal, theme, Typography } from "../../ui/kit";
import { useState } from "react";
import { del, get, type Example } from "../../api";
import { mono, StatusTag } from "../../components/bits";
import CodeEditor from "../../components/CodeEditor";
import { prettyBody } from "../../lib";

/** Saved responses ("examples") of a request. */
export default function ExamplesButton({ requestId }: { requestId: string }) {
  const { token } = theme.useToken();
  const qc = useQueryClient();
  const examples = useQuery({ queryKey: ["examples", requestId], queryFn: () => get<Example[]>(`/api/requests/${requestId}/examples`) });
  const [open, setOpen] = useState<Example | null>(null);
  const remove = useMutation({
    mutationFn: (id: string) => del(`/api/examples/${id}`),
    onSuccess: () => (qc.invalidateQueries({ queryKey: ["examples", requestId] }), setOpen(null)),
  });
  const list = examples.data ?? [];
  const body = open ? prettyBody(open.body) : null;
  return (
    <>
      <Dropdown
        trigger={["click"]}
        disabled={!list.length}
        menu={{
          items: list.map((e) => ({ key: e.id, label: e.name, extra: String(e.status) })),
          onClick: ({ key }) => setOpen(list.find((e) => e.id === key) ?? null),
        }}
      >
        <Button type="text" disabled={!list.length} title={list.length ? "" : "Send the request, then Save as example"}>
          Examples {list.length ? `(${list.length})` : ""}
        </Button>
      </Dropdown>
      <Modal
        title={open?.name}
        open={!!open}
        onCancel={() => setOpen(null)}
        width={760}
        footer={
          <Button danger loading={remove.isPending} onClick={() => open && remove.mutate(open.id)}>
            Delete example
          </Button>
        }
      >
        {open && (
          <>
            <div style={{ marginBottom: 8 }}>
              <StatusTag status={open.status} />
            </div>
            {open.headers.length > 0 && (
              <div style={{ marginBottom: 8 }}>
                {open.headers.map((h, i) => (
                  <div key={i} style={{ ...mono, fontSize: 12 }}>
                    <Typography.Text type="secondary">{h.key}:</Typography.Text> {h.value}
                  </div>
                ))}
              </div>
            )}
            <div style={{ height: 320, border: `1px solid ${token.colorBorder}`, borderRadius: 8, overflow: "hidden" }}>
              <CodeEditor value={body!.text} language={body!.language} readOnly />
            </div>
          </>
        )}
      </Modal>
    </>
  );
}
