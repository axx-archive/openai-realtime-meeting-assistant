import type { RootStackParamList } from './types';

export type NativeShellDestination = 'home' | 'video' | 'chat' | 'work' | 'files';
export type NativeShellLayout = 'compact' | 'sidebar';

export const NATIVE_SHELL_SIDEBAR_MIN_WIDTH = 744;
export const NATIVE_SHELL_SIDEBAR_MAX_FONT_SCALE = 1.35;

/**
 * Five first-class destinations — Home / Meet / Chat / Work / Files.
 *
 * This is the product bar for both iPhone and iPad. Network, Work Search, and
 * You are not shown in the nav, regardless of shellAccess. Meet dest stays
 * 'video'. Icons use custom stroke marks matching the product's line family:
 *   • Meet = 'meet-camera' (unmistakable video camera)
 *   • Chat = 'chat-bubble' (speech bubble, rounded rect + lower-left tail)
 *   • Work = 'work-pencil' (authoring pencil)
 *   • Files = 'stacked-sheets' (three overlapping rectangles)
 */
export const nativeShellDestinations = [
  { id: 'home', label: 'Home', route: 'Canvas', icon: 'home-mark' },
  { id: 'video', label: 'Meet', route: 'Meet', icon: 'meet-camera' },
  { id: 'chat', label: 'Chat', route: 'Chat', icon: 'chat-bubble' },
  { id: 'work', label: 'Work', route: 'WorkHome', icon: 'work-pencil' },
  { id: 'files', label: 'Files', route: 'Files', icon: 'stacked-sheets' },
] as const satisfies ReadonlyArray<{
  id: NativeShellDestination;
  label: string;
  route: keyof RootStackParamList;
  params?: RootStackParamList[keyof RootStackParamList];
  icon: string;
}>;

export type NativeShellAccess = 'core' | 'full';

export function nativeShellDestinationsForAccess(_access: unknown) {
  return nativeShellDestinations;
}

export function nativeShellDestinationAllowed(destination: NativeShellDestination, _access: unknown): boolean {
  return ['home', 'video', 'chat', 'work', 'files'].includes(destination);
}

/**
 * Route → destination mapping. Supporting Work surfaces remain owned by Work;
 * meeting records remain owned by Meet. Routes outside the five-destination
 * shell resolve to Home so the shell always retains a valid destination.
 */
const destinationRoutes: Partial<Record<keyof RootStackParamList, NativeShellDestination>> = {
  Canvas: 'home',
  Meet: 'video',
  Chat: 'chat',
  Files: 'files',
  Deck: 'work',
  Meetings: 'video',
  WorkHome: 'work',
  Board: 'work',
  AgentTeam: 'work',
  Memory: 'home',
  Intelligence: 'home',
  NetworkHome: 'home',
  NetworkDraft: 'home',
  NetworkPreview: 'home',
  NetworkRecruiterView: 'home',
  NetworkBlocks: 'home',
  WorkSearchHome: 'home',
  NetworkSearch: 'home',
  ContactInbox: 'home',
  YouHome: 'home',
  Profile: 'home',
  WorkRecord: 'work',
  Organizations: 'home',
  OrganizationPeople: 'home',
  CoworkerProfile: 'home',
  OrganizationRequests: 'home',
  OrganizationRecruiting: 'home',
  ContributionApprovals: 'home',
  Settings: 'home',
  Alerts: 'home',
};

const focusedRoutes = new Set<keyof RootStackParamList>([
  'Login',
  'Thread',
  'ChannelRiff',
  'Room',
  'CreateRoom',
  'NewConversation',
  'OSWeb',
  'DeckViewer',
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
  // Legacy Deck segment handling: rooms → Meet, threads → Chat, work → Work.
  if (route === 'Deck' && params && typeof params === 'object') {
    const segment = (params as { segment?: unknown }).segment;
    if (segment === 'rooms') return 'video';
    if (segment === 'threads') return 'chat';
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
      const params = 'params' in destination ? destination.params as RootStackParamList[keyof RootStackParamList] | undefined : undefined;
      navigate(destination.route, params);
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
