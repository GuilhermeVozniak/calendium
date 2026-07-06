import { Redirect } from 'expo-router';

/**
 * OAuth deep-link landing route. With @better-auth/expo the social sign-in flow
 * completes inline (the plugin opens the system browser and resolves on the
 * "calendium://" callback), so this route only needs to bounce any stray
 * callback deep link back to the app entry, which routes to inbox once signed in.
 */
export default function AuthCallback() {
  return <Redirect href="/" />;
}
