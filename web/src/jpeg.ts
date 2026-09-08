export async function reencodeJPEG(file: File): Promise<Blob> {
  const bmp = await createImageBitmap(file);
  try {
    const max = 1280;
    let w = bmp.width;
    let h = bmp.height;
    if (w > max || h > max) {
      if (w >= h) {
        h = Math.max(1, Math.round((h * max) / w));
        w = max;
      } else {
        w = Math.max(1, Math.round((w * max) / h));
        h = max;
      }
    }
    const canvas = document.createElement("canvas");
    canvas.width = w;
    canvas.height = h;
    const ctx = canvas.getContext("2d");
    if (!ctx) throw new Error("canvas");
    ctx.drawImage(bmp, 0, 0, w, h);
    const blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, "image/jpeg", 0.7));
    if (!blob) throw new Error("encode");
    return blob;
  } finally {
    bmp.close();
  }
}
