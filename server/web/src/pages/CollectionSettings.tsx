import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { App, Button, Input, Layout, Segmented, Space, Spin, Tabs, theme, Typography } from "../ui/kit";
import { useEffect, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { get, put, type CollectionSettings as Settings } from "../api";
import AuthEditor from "../components/AuthEditor";
import { PageHeader } from "../components/bits";
import CodeEditor from "../components/CodeEditor";
import KeyValueTable from "../components/KeyValueTable";
import { downloadCollection } from "../shell/CollectionActions";
import { useMe } from "../shell/session";

/** A collection's variables, auth and scripts, which its requests inherit. */
export default function CollectionSettings() {
  const { id } = useParams();
  const { token } = theme.useToken();
  const { message } = App.useApp();
  const nav = useNavigate();
  const qc = useQueryClient();
  const me = useMe();
  const saved = useQuery({ queryKey: ["collection-settings", id], queryFn: () => get<Settings>(`/api/collections/${id}/settings`), enabled: !!id });
  const [draft, setDraft] = useState<Settings | null>(null);
  const [tab, setTab] = useState("variables");
  const [script, setScript] = useState<"pre" | "post">("pre");
  useEffect(() => {
    if (saved.data) setDraft(saved.data);
  }, [saved.data]);
  const save = useMutation({
    mutationFn: (s: Settings) => put<Settings>(`/api/collections/${id}/settings`, s),
    onSuccess: (s) => {
      qc.setQueryData(["collection-settings", id], s);
      qc.invalidateQueries({ queryKey: ["collections"] });
      message.success("Saved");
    },
    onError: (e: Error) => message.error(e.message),
  });
  if (saved.isError) return <Layout.Content style={{ padding: 40 }}>Collection not found.</Layout.Content>;
  if (!draft) return <Spin style={{ margin: 40 }} />;
  const readOnly = me.data && !me.data.canEdit;
  const dirty = JSON.stringify(draft) !== JSON.stringify(saved.data);
  const set = (p: Partial<Settings>) => setDraft({ ...draft, ...p });
  const key = script === "pre" ? "preScript" : "postScript";
  return (
    <Layout.Content style={{ overflow: "auto", padding: "28px 40px", background: token.colorBgContainer, minHeight: "100%" }}>
      <PageHeader
        title={draft.name}
        subtitle={draft.kind === "api" ? "API collection · settings every request in it inherits" : "Collection · settings every request in it inherits"}
        extra={
          <>
            <Button onClick={() => nav(`/runner/${id}`)}>Run</Button>
            <Button onClick={() => downloadCollection(id!)}>Export</Button>
            <Button type="primary" disabled={!dirty || !!readOnly} loading={save.isPending} onClick={() => save.mutate(draft)}>
              Save
            </Button>
          </>
        }
      />
      <Tabs
        activeKey={tab}
        onChange={setTab}
        items={[
          {
            key: "variables",
            label: `Variables${draft.variables.length ? ` (${draft.variables.length})` : ""}`,
            children: (
              <>
                <Typography.Paragraph type="secondary">
                  Available as {"{{name}}"} in every request of this collection. An environment's variable with the same name wins; iteration data from the runner wins over both.
                </Typography.Paragraph>
                <KeyValueTable rows={draft.variables} onChange={(variables) => set({ variables })} readOnly={!!readOnly} keyLabel="Variable" />
              </>
            ),
          },
          {
            key: "auth",
            label: "Authorization",
            children: (
              <>
                <Typography.Paragraph type="secondary">Requests set to "Inherit from collection" use this.</Typography.Paragraph>
                <AuthEditor auth={draft.auth} onChange={(auth) => set({ auth })} inheritLabel="None set (requests use their own)" readOnly={!!readOnly} collectionId={draft.id} />
              </>
            ),
          },
          {
            key: "scripts",
            label: "Scripts",
            children: (
              <Space direction="vertical" style={{ width: "100%" }} size={12}>
                <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
                  <Segmented value={script} onChange={(v) => setScript(v as "pre" | "post")} options={[{ value: "pre", label: "Pre-request" }, { value: "post", label: "Post-response" }]} />
                  <Typography.Text type="secondary" style={{ fontSize: 13 }}>
                    Runs before each request's own script. Postman's pm.* API works here.
                  </Typography.Text>
                </div>
                <div style={{ height: 320, border: `1px solid ${token.colorBorder}`, borderRadius: 8, overflow: "hidden" }}>
                  <CodeEditor path={`requara://collection/${id}-${script}.js`} language="javascript" value={draft[key]} onChange={(v) => set({ [key]: v })} readOnly={!!readOnly} />
                </div>
              </Space>
            ),
          },
          {
            key: "overview",
            label: "Description",
            children: <Input.TextArea rows={8} value={draft.description} onChange={(e) => set({ description: e.target.value })} placeholder="What this collection is for" disabled={!!readOnly} />,
          },
        ]}
      />
    </Layout.Content>
  );
}
