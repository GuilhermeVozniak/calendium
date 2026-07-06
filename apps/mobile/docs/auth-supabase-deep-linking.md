# Supabase Auth + Deep Linking (Expo)

This app uses **Supabase Auth** with **Expo** and **deep linking** for Google login.

There are **two supported flows**:

- **Expo Go (local dev, no custom scheme)**
- **Dev/Production build (custom app scheme `calendium://`)**

If this is misconfigured, you will see errors like:

> Safari cannot open the page because the address is invalid

on the iOS simulator.

This document explains **why** that happens and how to set up both flows correctly.

---

## 1. Root concepts

### 1.1. Custom scheme vs. Expo Go

- Our code uses `expo-auth-session` and `expo-web-browser` to perform OAuth.
- For **production/dev builds**, we want a custom app scheme, e.g. `calendium://`.
- On redirect, iOS asks: _"Which installed app owns this scheme?"_.
- **Expo Go does not own your custom scheme** (`calendium://`), so the OS may try to open Safari instead. Safari cannot handle `calendium://` and shows the "invalid address" error.
- In contrast, with a **dev client / standalone build**, iOS knows that your app handles `calendium://` and opens it correctly.

### 1.2. Current app code

Key pieces:

- `app.json`
  - `"scheme": "calendium"`
- `lib/supabase.ts`
  - Creates a Supabase client with `AsyncStorage` and `detectSessionInUrl: false`.
- `context/auth.tsx`
  - Uses `makeRedirectUri({ scheme: process.env.EXPO_PUBLIC_SCHEME })`.
  - Uses `supabase.auth.signInWithOAuth({ provider, options: { redirectTo, skipBrowserRedirect: true } })`.
  - Opens the auth session with `WebBrowser.openAuthSessionAsync(data.url, redirectTo)`.
  - Parses the returned URL and calls `supabase.auth.setSession({ access_token, refresh_token })`.

Env variables (example):

```bash
EXPO_PUBLIC_SUPABASE_URL=...
EXPO_PUBLIC_SUPABASE_ANON_KEY=...
EXPO_PUBLIC_SCHEME=calendium
```

Our **Supabase Auth Redirect URLs** (in Dashboard → Authentication → URL Configuration):

```text
calendium://**
```

This is correct for **custom scheme builds**, but not for **Expo Go**.

---

## 2. Option A – Dev/Production build (recommended for real apps)

This is the **production-ready** setup using the custom scheme `calendium://`.

### 2.1. Supabase configuration

In **Supabase Dashboard → Authentication → URL Configuration**:

- **Site URL**:

  ```text
  calendium://
  ```

- **Redirect URLs**:

  ```text
  calendium://**
  ```

In **Supabase Dashboard → Authentication → Providers → Google**:

- Enable **Google**.
- In **Google Cloud Console**, configure the **Authorized redirect URI** as the Supabase callback, e.g.:

  ```text
  https://<project-ref>.supabase.co/auth/v1/callback
  ```

- Copy the Client ID and Client Secret into Supabase.

### 2.2. Expo configuration

`app.json`:

```json
{
  "expo": {
    "scheme": "calendium",
    ...
  }
}
```

Environment variables:

```bash
EXPO_PUBLIC_SCHEME=calendium
EXPO_PUBLIC_SUPABASE_URL=...
EXPO_PUBLIC_SUPABASE_ANON_KEY=...
```

### 2.3. Building and running

You **must** run a dev build or standalone app (not Expo Go) so iOS knows your app owns `calendium://`.

Using EAS:

```bash
npm install -g eas-cli

# In project root
eas login
eas build:configure

# Dev build for iOS (runs on a simulator or device)
eas build --profile development --platform ios
```

Install the resulting app on your simulator/device, open it, and test:

1. Tap **Continue with Google**.
2. Browser opens and you log in.
3. iOS redirects to `calendium://...`.
4. Your app resumes, `AuthProvider` parses the URL and calls `supabase.auth.setSession`.
5. The UI shows the authenticated state.

If you see the Safari "invalid address" error in this scenario, double-check:

- The app you’re running is the **dev build / standalone app**, not Expo Go.
- `EXPO_PUBLIC_SCHEME` matches `scheme` in `app.json`.
- `calendium://**` is in Supabase **Redirect URLs**.

---

## 3. Option B – Expo Go (local dev using proxy)

If you want to keep using **Expo Go** on simulator/device for local development, you can’t rely on `calendium://` because Expo Go does not own that scheme.

Instead, you use **Expo Auth Session’s proxy**:

- Redirect uses a hosted `https://auth.expo.io/...` URL.
- Supabase redirects there.
- Expo’s proxy sends the result back into Expo Go.

### 3.1. Code changes (conceptual)

In `AuthProvider`:

- Switch from scheme-based `makeRedirectUri` to proxy-based configuration. Example (conceptual):

  ```ts
  const redirectTo = makeRedirectUri({
    // Do not set `scheme` here while using Expo Go proxy
    // Use defaults so expo-auth-session generates an auth.expo.io URL
  });
  ```

- Keep `skipBrowserRedirect: true` and `WebBrowser.openAuthSessionAsync`.

- Log `redirectTo` once during development to see the actual URL:

  ```ts
  console.log('Expo Go redirectTo:', redirectTo);
  ```

### 3.2. Supabase redirect URL for Expo Go

After you know the exact `redirectTo` value (will look like `https://auth.expo.io/@<username>/<slug>`):

1. Go to **Supabase Dashboard → Authentication → URL Configuration → Redirect URLs**.
2. Add the **full** Expo proxy URL, for example:

   ```text
   https://auth.expo.io/@your-username/calendium
   ```

3. Keep the scheme-based redirect for production as well, if you want to support both:

   ```text
   calendium://**
   https://auth.expo.io/@your-username/calendium
   ```

Now, when testing in Expo Go:

1. Tap **Continue with Google**.
2. Browser opens, you complete Google sign-in.
3. Supabase redirects to the **Expo proxy URL**.
4. Expo proxy hands the result back to Expo Go.
5. `AuthProvider` receives the final URL, parses tokens, and calls `supabase.auth.setSession`.

### 3.3. Switching between flows

You can:

- Use **proxy mode** while in Expo Go (local dev).
- Use **scheme mode** in dev/production builds.

A common pattern is to branch based on `Constants.appOwnership` or `__DEV__`, but that adds complexity. For now, decide which workflow your team prefers and document it.

---

## 4. Quick troubleshooting checklist

If Google login doesnt work, check:

- **Environment**
  - Are you running **Expo Go** or a **dev/standalone build**?
  - Does that match the configuration (proxy vs. scheme)?

- **Env vars**
  - `EXPO_PUBLIC_SUPABASE_URL` and `EXPO_PUBLIC_SUPABASE_ANON_KEY` are correct.
  - `EXPO_PUBLIC_SCHEME` matches `app.json` `scheme` when using the custom scheme.

- **Supabase URLs**
  - `calendium://**` present for scheme-based builds.
  - Expo proxy URL present when using Expo Go.

- **Google provider**
  - Enabled in Supabase with correct Client ID/secret.
  - Google Cloud Console allowed redirect: `https://<project-ref>.supabase.co/auth/v1/callback`.

With this document, a developer should be able to:

- Understand the Safari / invalid address error on iOS.
- Correctly configure Supabase and Expo for **Expo Go** development.
- Correctly configure Supabase and Expo for **dev/prod builds** using the real app scheme.
