import { ImageManipulator, SaveFormat } from 'expo-image-manipulator';
import {
  AVATAR_ENCODING_PASSES,
  avatarDataURLFits,
  jpegAvatarDataURL,
} from './avatarEncoding';

export async function prepareAvatarDataURL(
  uri: string,
  sourceWidth?: number,
): Promise<string> {
  for (const pass of AVATAR_ENCODING_PASSES) {
    const width = sourceWidth ? Math.min(sourceWidth, pass.dimension) : pass.dimension;
    const context = ImageManipulator.manipulate(uri);
    context.resize({ width, height: null });
    const rendered = await context.renderAsync();
    const result = await rendered.saveAsync({
      base64: true,
      compress: pass.compression,
      format: SaveFormat.JPEG,
    });
    if (!result.base64) continue;
    const dataURL = jpegAvatarDataURL(result.base64);
    if (avatarDataURLFits(dataURL)) return dataURL;
  }

  throw new Error('That photo could not be made small enough. Please choose another image.');
}
