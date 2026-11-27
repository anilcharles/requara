// The icons the app used from Ant Design, as Lucide line icons under the
// same names. Like Ant's, they size from style.fontSize (default 14px) and
// take colour from style.color or the text around them.
import {
  ArrowDown,
  ArrowUp,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  Code2,
  Copy,
  Download,
  Eraser,
  ExternalLink,
  Inbox,
  Info,
  KeyRound,
  Lock,
  Mail,
  Paperclip,
  Plus,
  Search,
  Settings,
  SlidersHorizontal,
  Trash2,
  TriangleAlert,
  User,
  X,
  XCircle,
  ChevronLeft,
  Building2,
  LayoutDashboard,
  ScrollText,
  Server,
  Users,
  type LucideIcon,
} from "lucide-react";
import type { CSSProperties, MouseEventHandler } from "react";

type IconProps = { style?: CSSProperties; className?: string; onClick?: MouseEventHandler; "aria-label"?: string; spin?: boolean };

const wrap = (Icon: LucideIcon) =>
  function AntIcon({ style, ...rest }: IconProps) {
    const size = typeof style?.fontSize === "number" ? style.fontSize : 14;
    const { fontSize, ...css } = style ?? {};
    void fontSize;
    return <Icon size={size} strokeWidth={1.7} style={{ verticalAlign: "-0.15em", flexShrink: 0, ...css }} {...rest} />;
  };

export const ArrowDownOutlined = wrap(ArrowDown);
export const ArrowUpOutlined = wrap(ArrowUp);
export const CaretDownOutlined = wrap(ChevronDown);
export const CaretRightOutlined = wrap(ChevronRight);
export const CheckCircleFilled = wrap(CheckCircle2);
export const ClearOutlined = wrap(Eraser);
export const CloseCircleFilled = wrap(XCircle);
export const CloseOutlined = wrap(X);
export const CodeOutlined = wrap(Code2);
export const CopyOutlined = wrap(Copy);
export const DeleteOutlined = wrap(Trash2);
export const DownloadOutlined = wrap(Download);
export const ExportOutlined = wrap(ExternalLink);
export const InboxOutlined = wrap(Inbox);
export const InfoCircleOutlined = wrap(Info);
export const KeyOutlined = wrap(KeyRound);
export const LeftOutlined = wrap(ChevronLeft);
export const LockOutlined = wrap(Lock);
export const MailOutlined = wrap(Mail);
export const PaperClipOutlined = wrap(Paperclip);
export const PlusOutlined = wrap(Plus);
export const SearchOutlined = wrap(Search);
export const SettingOutlined = wrap(Settings);
export const SlidersOutlined = wrap(SlidersHorizontal);
export const UserOutlined = wrap(User);
export const WarningFilled = wrap(TriangleAlert);
export const ApartmentOutlined = wrap(Building2);
export const AuditOutlined = wrap(ScrollText);
export const CloudServerOutlined = wrap(Server);
export const DashboardOutlined = wrap(LayoutDashboard);
export const TeamOutlined = wrap(Users);
