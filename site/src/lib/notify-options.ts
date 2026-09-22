/**
 * The ways a reader can turn their watch feed URL into a notification, stated
 * ONCE.
 *
 * Two surfaces render this list: the "Get notified" tab on /watching (the
 * picker, which shows `label`/`blurb` and links into the docs page) and
 * /docs/notifications (the full step-by-step). A second copy would let the tab
 * offer an option the docs page has no instructions for, which is the shape of
 * every "how do I actually use this" complaint the vague list it replaced got.
 *
 * The order is the recommendation order: simplest first, and everything that
 * needs no third-party account before everything that does. `needsAccount`
 * means an account with SOMEBODY ELSE (Telegram, Slack, Discord, IFTTT) - it is
 * never an account here, because there is no account here. Blogtrottr takes an
 * email address without one, so it is false.
 *
 * Facts verified 2026-09; the cadence numbers are the providers' own published
 * free-tier behaviour and are stated rather than estimated.
 */

/** Which of the page's URLs a set of steps pastes. */
export type FeedKind = 'webcal' | 'atom' | 'json'

/** What each URL is CALLED, in the reader's words. Both surfaces label the
    same three links - the Get notified tab's anchors and copy rows, and the
    docs page's "which link to copy" table and per-option chip - so a reader
    following a step that says "copy the Calendar link" finds a control spelled
    exactly that. */
export const FEED_LABELS: Record<FeedKind, string> = {
  webcal: 'Calendar link',
  atom: 'Atom feed',
  json: 'JSON Feed',
}

/** The id is also the docs page's anchor, so it is slug-shaped. */
export type NotifyOptionID =
  | 'calendar'
  | 'rss-app'
  | 'email'
  | 'telegram'
  | 'slack'
  | 'discord'
  | 'self-hosted'
  | 'automation'

export interface NotifyOption {
  id: NotifyOptionID
  /** Short heading, for the picker and the docs section. */
  label: string
  /** Needs an account with a THIRD PARTY, not with this site. */
  needsAccount: boolean
  /** Which URL the steps paste. */
  feed: FeedKind
  /** One sentence, for the tab's picker. */
  blurb: string
  /** Concrete, numbered steps for the docs page. */
  steps: string[]
  /** Refresh cadence, free-tier limits, caveats. */
  notes?: string[]
}

export const NOTIFY_OPTIONS: readonly NotifyOption[] = [
  {
    id: 'calendar',
    label: 'Calendar',
    needsAccount: false,
    feed: 'webcal',
    blurb: 'Subscribe to a calendar and see every release date in the app you already check daily.',
    steps: [
      'Copy the Calendar link from the Get notified tab on the Watching page.',
      'Google Calendar: in the left sidebar, open the "+" beside Other calendars, choose From URL, paste the link and click Add calendar.',
      'Apple Calendar on a Mac: File > New Calendar Subscription, paste the link, click Subscribe and set Auto-refresh.',
      'iPhone or iPad: Settings > Calendar > Accounts > Add Account > Other > Add Subscribed Calendar, then paste the link.',
      'Outlook: Add calendar > Subscribe from web, paste the link and add it.',
    ],
    notes: [
      'Each release date becomes one all-day event; any alert or reminder is the calendar app’s own setting, not ours.',
      'Google refreshes a subscribed calendar roughly every 12 to 24 hours; Apple refreshes at the interval you pick.',
      'A book with no announced release date has no date to put in a calendar, so it is not in this feed. The Atom feed still lists it.',
    ],
  },
  {
    id: 'rss-app',
    label: 'RSS app',
    needsAccount: false,
    feed: 'atom',
    blurb: 'Add the feed to a reader that can raise a notification when something new arrives.',
    steps: [
      'Copy the Atom feed link from the Get notified tab on the Watching page.',
      'NetNewsWire (Mac and iOS, free and open source): add the feed, then on iOS touch and hold the feed, choose Get Info and turn on "Notify About New Articles".',
      'Feeder (Android, from F-Droid or Google Play): add the feed, then enable notifications for that feed in its settings.',
      'Thunderbird: add a Feeds account, then subscribe to the URL from it.',
      'Feedbro (a Chrome or Firefox extension): add the feed, then add a Rule that shows a desktop notification for its new items.',
      'Vivaldi has a feed reader built in, so the browser can hold the subscription with no extension at all.',
    ],
    notes: [
      'A notification only fires when the app next checks the feed, so the app decides how promptly you hear about a release.',
      'NetNewsWire keeps per-feed notification settings on the device they were set on; they do not sync between your devices.',
    ],
  },
  {
    id: 'email',
    label: 'Email',
    needsAccount: false,
    feed: 'atom',
    blurb: 'Have a free feed-to-email service send you a message, with no account to create.',
    steps: [
      'Copy the Atom feed link from the Get notified tab on the Watching page.',
      'Open blogtrottr.com.',
      'Paste the feed URL into the feed box and type your email address.',
      'Choose realtime delivery or a digest interval.',
      'Click Feed Me, then open the opt-in email and click the confirmation link.',
    ],
    notes: [
      'Blogtrottr holds your email address, not this site: we never see it and store nothing.',
      'Their free tier is ad-supported, and you can unsubscribe from the link in any message they send.',
    ],
  },
  {
    id: 'telegram',
    label: 'Telegram',
    needsAccount: true,
    feed: 'atom',
    blurb: 'A Telegram bot posts new releases into a chat with you.',
    steps: [
      'Copy the Atom feed link from the Get notified tab on the Watching page.',
      'In Telegram, open a chat with @TheFeedReaderBot.',
      'Send `/add` followed by the feed URL.',
      'Confirm when the bot asks.',
      'Use `/list` to see your feeds and `/remove` to drop one.',
    ],
    notes: ['On the free tier the bot checks each feed every 12 hours.'],
  },
  {
    id: 'slack',
    label: 'Slack',
    needsAccount: true,
    feed: 'atom',
    blurb: 'Slack posts new releases into a channel using its built-in RSS app.',
    steps: [
      'Copy the Atom feed link from the Get notified tab on the Watching page.',
      'In the channel you want the posts in, type `/feed subscribe <url>` with your feed URL.',
      'Use `/feed list` to see the channel’s feeds and `/feed remove <id>` to drop one.',
    ],
    notes: ['The RSS app has to be enabled in the workspace; an admin can turn it on.'],
  },
  {
    id: 'discord',
    label: 'Discord',
    needsAccount: true,
    feed: 'atom',
    blurb: 'A Discord bot posts new releases into a channel on a server you manage.',
    steps: [
      'Copy the Atom feed link from the Get notified tab on the Watching page.',
      'Open monitorss.xyz and choose Log in with Discord.',
      'Choose Add to Server and pick a server you manage.',
      'Create or pick the channel the posts should land in; a private channel is fine.',
      'Choose Add feed, paste the URL, pick that channel and save.',
    ],
    notes: [
      'MonitoRSS delivers every 20 minutes on its free tier.',
      'The bot needs permission to post in the channel you pick.',
      'Alternative: an IFTTT applet from an RSS feed to a Discord webhook.',
    ],
  },
  {
    id: 'self-hosted',
    label: 'Self-hosted',
    needsAccount: false,
    feed: 'atom',
    blurb: 'Run the polling yourself and push to whatever you already use.',
    steps: [
      'Copy the Atom feed link from the Get notified tab on the Watching page.',
      'Miniflux: subscribe to the feed, then in that feed’s settings enable an integration - ntfy, Pushover, Gotify, Telegram, Apprise, Matrix or a plain webhook - to be pushed on new entries.',
      'Home Assistant: add the `feedreader` integration with the feed URL, then write an automation on the feedreader event that calls your companion app’s notify service.',
      'n8n or Node-RED: an RSS trigger node feeding any notification node you like.',
    ],
    notes: [
      'Apprise cannot poll a feed on its own: it needs Miniflux, FreshRSS or a cron job in front of it.',
      'FreshRSS works well as a reader, but pushing a notification out of it needs an extension.',
    ],
  },
  {
    id: 'automation',
    label: 'Automation',
    needsAccount: true,
    feed: 'atom',
    blurb: 'An automation service watches the feed and sends a phone notification, email or webhook.',
    steps: [
      'Copy the Atom feed link from the Get notified tab on the Watching page.',
      'IFTTT: create an applet with the trigger "RSS Feed > New feed item" and your feed URL.',
      'IFTTT: for the action choose "Notifications > Send a notification from the IFTTT app", or send an email or a webhook instead.',
      'Zapier: create a Zap with the trigger "RSS by Zapier > New Item in Feed" and your feed URL, then any action you like.',
    ],
    notes: [
      'The IFTTT free tier allows 2 applets and polls the feed hourly; Pro polls every few minutes.',
    ],
  },
]

/**
 * What is true of EVERY option above, so no set of steps has to restate it. The
 * first two are the consequence of the feed being stateless: the URL IS the
 * subscription, which is what lets the server keep no account and no
 * subscription store.
 */
export const NOTIFY_INTRO_NOTES: readonly string[] = [
  'The URL is the subscription. It carries the series you watch, so whenever you add or remove one, copy the new URL and paste it into whatever you set up below.',
  'Anyone who has the URL can see which series it lists, so treat it like a private link: share it only if you mean to.',
  'This site never stores an email address, a phone number or any other contact detail. Whatever you set up below is between you and that service.',
  'A book you marked "not interested" on the Watching page still appears in the feed, because the URL carries only the list of series, not your per-book marks.',
]

/** Where the docs page documents one option. */
export function notifyDocHref(id: NotifyOptionID): string {
  return `/docs/notifications#${id}`
}
