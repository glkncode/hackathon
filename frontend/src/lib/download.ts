export async function saveDownload(response: Response, fallbackName: string) {
  const disposition = response.headers.get('Content-Disposition') ?? '';
  let filename = fallbackName;
  const encoded = /filename\*=UTF-8''([^;]+)/i.exec(disposition)?.[1];
  const plain = /filename="([^"]+)"|filename=([^;]+)/i.exec(disposition);
  try {
    filename = encoded ? decodeURIComponent(encoded) : (plain?.[1] ?? plain?.[2] ?? fallbackName);
  } catch {
    /* Use the safe fallback when a server filename is malformed. */
  }
  filename = Array.from(filename)
    .map((char) => (char.charCodeAt(0) < 32 || char === '/' || char === '\\' ? '_' : char))
    .join('');
  const url = URL.createObjectURL(await response.blob());
  const link = document.createElement('a');
  link.href = url;
  link.download = filename;
  document.body.append(link);
  link.click();
  link.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
