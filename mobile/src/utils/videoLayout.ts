export type VideoDimensions = Readonly<{
  width: number;
  height: number;
}>;

export type ContainedVideoLabelPosition = Readonly<{
  left: number;
  bottom: number;
  maxWidth: number;
}>;

/** A MediaStream URL may stay stable while iOS swaps its underlying camera track. */
export function nativeVideoRenderIdentity(
  streamURL: string | undefined,
  trackId: string | undefined,
  mode: 'camera' | 'screen',
  framingRevision = '',
): string {
  return `${mode}:${streamURL ?? ''}:${trackId ?? 'pending'}:${framingRevision}`;
}

/** Sizes a video surface to its pixels without cropping or letterboxing. */
export function fittedVideoDimensions(
  container: VideoDimensions,
  video: VideoDimensions,
): VideoDimensions | null {
  const values = [container.width, container.height, video.width, video.height];
  if (values.some((value) => !Number.isFinite(value))
    || container.width <= 0
    || container.height <= 0
    || video.width <= 0
    || video.height <= 0) {
    return null;
  }

  const scale = Math.min(container.width / video.width, container.height / video.height);
  return {
    width: video.width * scale,
    height: video.height * scale,
  };
}

/**
 * Places an overlay inside the visible pixels of an object-fit: contain video.
 * The native RTCView fills its container, so ordinary absolute positioning is
 * relative to the letterbox rather than the rendered video frame.
 */
export function containedVideoLabelPosition(
  container: VideoDimensions,
  video: VideoDimensions,
  inset: number,
): ContainedVideoLabelPosition | null {
  const values = [container.width, container.height, video.width, video.height, inset];
  if (values.some((value) => !Number.isFinite(value))
    || container.width <= 0
    || container.height <= 0
    || video.width <= 0
    || video.height <= 0
    || inset < 0) {
    return null;
  }

  const fitted = fittedVideoDimensions(container, video);
  if (!fitted) return null;
  const renderedWidth = fitted.width;
  const renderedHeight = fitted.height;

  return {
    left: Math.max(inset, (container.width - renderedWidth) / 2 + inset),
    bottom: Math.max(inset, (container.height - renderedHeight) / 2 + inset),
    maxWidth: Math.max(0, renderedWidth - inset * 2),
  };
}
