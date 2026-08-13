import type { RootStackParamList } from './types';

export type NativeShellDestination = 'home' | 'video' | 'chat' | 'work' | 'network' | 'work-search' | 'you';
export type NativeShellLayout = 'compact' | 'sidebar';

export const NATIVE_SHELL_SIDEBAR_MIN_WIDTH = 744;
export const NATIVE_SHELL_SIDEBAR_MAX_FONT_SCALE = 1.35;

export const nativeShellDestinations = [
  { id: 'home', label: 'Home', route: 'Canvas', icon: 'house.fill' },
  { id: 'video', label: 'Video', route: 'Deck', params: { segment: 'rooms' }, icon: 'video.fill' },
  { id: 'chat', label: 'Chat', route: 'Deck', params: { segment: 'threads' }, icon: 'bubble.left.and.bubble.right.fill' },
  { id: 'work', label: 'Work', route: 'WorkHome', icon: 'rectangle.3.group.fill' },
  { id: 'network', label: 'Network', route: 'NetworkHome', icon: 'point.3.connected.trianglepath.dotted' },
  { id: 'work-search', label: 'Work Search', route: 'WorkSearchHome', icon: 'magnifyingglass' },
  { id: 'you', label: 'You', route: 'YouHome', icon: 'person.crop.circle.fill' },
] as const satisfies ReadonlyArray<{
  id: NativeShellDestination;
  label: string;
  route: keyof RootStackParamList;
  params?: RootStackParamList[keyof RootStackParamList];
  icon: string;
}>;

export type NativeShellAccess = 'core' | 'full';

const coreShellDestinationIDs = new Set<NativeShellDestination>(['home', 'video', 'chat']);

export function nativeShellDestinationsForAccess(access: unknown) {
  return access === 'full'
    ? nativeShellDestinations
    : nativeShellDestinations.filter(({ id }) => coreShellDestinationIDs.has(id));
}

export function nativeShellDestinationAllowed(destination: NativeShellDestination, access: unknown): boolean {
  return access === 'full' || coreShellDestinationIDs.has(destination);
}

const destinationRoutes: Partial<Record<keyof RootStackParamList, NativeShellDestination>> = {
  Canvas: 'home',
  Deck: 'chat',
  Meetings: 'video',
  WorkHome: 'work',
  Board: 'work',
  Files: 'work',
  AgentTeam: 'work',
  Memory: 'work',
  Intelligence: 'work',
  NetworkHome: 'network',
  NetworkDraft: 'network',
  NetworkPreview: 'network',
  NetworkRecruiterView: 'network',
  NetworkBlocks: 'network',
  WorkSearchHome: 'work-search',
  NetworkSearch: 'work-search',
  ContactInbox: 'work-search',
  YouHome: 'you',
  Profile: 'you',
  WorkRecord: 'you',
  Organizations: 'you',
  OrganizationPeople: 'you',
  CoworkerProfile: 'you',
  OrganizationRequests: 'you',
  OrganizationRecruiting: 'you',
  ContributionApprovals: 'you',
  Settings: 'you',
  Alerts: 'you',
};

const focusedRoutes = new Set<keyof RootStackParamList>([
  'Login',
  'Thread',
  'Room',
  'CreateRoom',
  'NewConversation',
  'OSWeb',
]);

export function nativeShellLayout(
  width: number,
  supportsSidebar = true,
  fontScale = 1,
): NativeShellLayout {
  const largeText = Number.isFinite(fontScale) && fontScale >= NATIVE_SHELL_SIDEBAR_MAX_FONT_SCALE;
  return supportsSidebar && !largeText && Number.isFinite(width) && width >= NATIVE_SHELL_SIDEBAR_MIN_WIDTH
    ? 'sidebar'
    : 'compact';
}

export function nativeShellDestinationForRoute(
  route: keyof RootStackParamList | undefined,
  params?: unknown,
): NativeShellDestination {
  if (route === 'Deck' && params && typeof params === 'object') {
    const segment = (params as { segment?: unknown }).segment;
    if (segment === 'rooms') return 'video';
    if (segment === 'work') return 'work';
  }
  return (route && destinationRoutes[route]) || 'home';
}

export function nativeShellVisibleForRoute(route: keyof RootStackParamList | undefined): boolean {
  return Boolean(route && !focusedRoutes.has(route));
}

export function nativeShellSelectionAnnouncement(destination: NativeShellDestination): string {
  const selected = nativeShellDestinations.find(({ id }) => id === destination);
  return `${selected?.label ?? 'Home'} selected`;
}

export function createNativeShellSelectionCoordinator(
  announce: (message: string) => void,
  initial: NativeShellDestination = 'home',
) {
  let committed = initial;
  return {
    select(
      destination: (typeof nativeShellDestinations)[number],
      navigate: (route: keyof RootStackParamList, params?: RootStackParamList[keyof RootStackParamList]) => void,
    ) {
      navigate(destination.route, 'params' in destination ? destination.params : undefined);
    },
    commit(route: keyof RootStackParamList | undefined, params?: unknown) {
      // Full-screen and otherwise unmapped routes belong to the destination
      // that opened them. They hide shell chrome but must not silently select
      // Home or announce a destination the user did not choose.
      const next = route === 'Deck'
        ? nativeShellDestinationForRoute(route, params)
        : route && destinationRoutes[route];
      if (!next) return committed;
      if (next !== committed) {
        committed = next;
        announce(nativeShellSelectionAnnouncement(next));
      }
      return next;
    },
    current() { return committed; },
  };
}
