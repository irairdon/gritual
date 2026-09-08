import { useEffect, useState } from "react";
import { toDataURL } from "qrcode";

export default function InviteQR({ url }: { url: string }) {
  const [src, setSrc] = useState("");
  useEffect(() => {
    let cancelled = false;
    toDataURL(url, { margin: 1, width: 160, color: { dark: "#1c1917", light: "#ffffff" } }).then(
      (s) => {
        if (!cancelled) setSrc(s);
      },
      () => {
        if (!cancelled) setSrc("");
      },
    );
    return () => {
      cancelled = true;
    };
  }, [url]);
  if (!src) return null;
  return <img alt="Invite QR code" width={160} height={160} src={src} className="border border-stone-200 bg-white" />;
}
