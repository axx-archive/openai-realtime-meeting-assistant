/** The server accepts avatar data URLs up to 192 KiB. Leave room for headers. */
export const MAX_AVATAR_DATA_URL_LENGTH = 188 * 1024;

export type AvatarEncodingPass = {
  dimension: number;
  compression: number;
};

export const AVATAR_ENCODING_PASSES: readonly AvatarEncodingPass[] = [
  { dimension: 512, compression: 0.76 },
  { dimension: 384, compression: 0.64 },
  { dimension: 320, compression: 0.54 },
  { dimension: 256, compression: 0.44 },
  { dimension: 192, compression: 0.34 },
];

export function jpegAvatarDataURL(base64: string): string {
  return `data:image/jpeg;base64,${base64.replace(/\s+/g, '')}`;
}

export function avatarDataURLFits(dataURL: string): boolean {
  return dataURL.length <= MAX_AVATAR_DATA_URL_LENGTH;
}
