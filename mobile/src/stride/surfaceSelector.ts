import type { StrideSurfaceName, StrideSurfaceProjection } from './models';

export type StrideSurfaceResourceSelector = Readonly<{ person: string }>;

export function buildStrideSurfacePath(
  surface: StrideSurfaceName,
  selector?: StrideSurfaceResourceSelector,
): string {
  const base = `/api/stride/v1/mobile/surfaces/${encodeURIComponent(surface)}`;
  if (surface !== 'coworker-profile') {
    if (selector !== undefined) throw new Error('This surface does not accept a resource selector.');
    return base;
  }
  if (!selector || Object.keys(selector).length !== 1 || !Object.prototype.hasOwnProperty.call(selector, 'person')) {
    throw new Error('A closed coworker resource selector is required.');
  }
  const person = selector.person;
  if (typeof person !== 'string' || !/^[A-Za-z0-9][A-Za-z0-9._:/-]{0,191}$/.test(person)) {
    throw new Error('The coworker resource selector is invalid.');
  }
  return `${base}?person=${encodeURIComponent(person)}`;
}

export function bindStrideSurfaceResource(
  surface: StrideSurfaceName,
  selector: StrideSurfaceResourceSelector | undefined,
  projection: StrideSurfaceProjection,
): StrideSurfaceProjection {
  // Validate the same closed selector contract used to build the request,
  // including rejecting selectors accidentally attached to other surfaces.
  buildStrideSurfacePath(surface, selector);
  if (surface !== 'coworker-profile' || projection.availability === 'unavailable') return projection;
  if (projection.items.length !== 1 || projection.items[0].id !== selector?.person) {
    throw new Error('The coworker projection does not match the selected resource.');
  }
  return projection;
}
