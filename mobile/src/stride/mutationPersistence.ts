import * as SecureStore from 'expo-secure-store';
import type { StrideMutationPersistence } from './mutationAuthority';

export const strideMutationSecurePersistence: StrideMutationPersistence = {
  read: (key) => SecureStore.getItemAsync(key),
  write: (key, value) => SecureStore.setItemAsync(key, value),
  remove: (key) => SecureStore.deleteItemAsync(key),
};
