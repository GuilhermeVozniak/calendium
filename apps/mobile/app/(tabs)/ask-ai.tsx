import { Redirect } from 'expo-router';

/**
 * Placeholder route for the "Ask AI" tab bar item. The tab's `tabPress`
 * listener (see `(tabs)/_layout.tsx`) intercepts the press and pushes the
 * real modal at `/ask-ai` instead, so this component only renders if that
 * interception somehow doesn't happen — in which case it just sends the
 * user back to the inbox rather than showing a blank tab.
 */
export default function AskAiTabPlaceholder() {
  return <Redirect href="/(tabs)/inbox" />;
}
