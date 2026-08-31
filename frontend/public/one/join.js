/**
 * join.js — the invitation page (BRA-1475, a rewrite in place of BRA-1439 Story 5).
 *
 * WHAT WAS WRONG WITH THE PAGE THIS REPLACES, because the shape of this file is
 * the answer to it. The old page accepted the invitation the moment it loaded,
 * using whoever happened to be signed in, without asking. For an administrator
 * clicking an invitation they had just sent — the most common arrival — that
 * always failed, because the seat was not theirs. For a person with no session
 * it offered three buttons, and every one of them led into the old Vue
 * application, where none could finish: the task server's registration route
 * refuses everybody, and setting a password on an account nobody had made is
 * not a way in.
 *
 * SO THIS PAGE DOES NOTHING ON ITS OWN. On load it does exactly two things: it
 * reads whether anybody is signed in, and it asks the paid-account service what
 * organisation and team the handle in the address names. Neither consumes
 * anything, neither spends the token, and neither takes a seat. Everything else
 * happens when the person presses the one button.
 *
 * THE TWELVE-STEP JOURNEY, and which steps are this file's:
 *
 *   1-2  the administrator invites, the mail arrives, the person presses its
 *        button                                            (elsewhere, already live)
 *   3    this page opens and does nothing on its own                    HERE
 *   4    it asks for the organisation and team names                    HERE
 *   5-6  one screen: a heading, one sentence, three fields, one button;
 *        the address is filled in and cannot be changed                 HERE
 *   7    the username, password and token go to the paid-account service HERE
 *   8-10 the service checks the token, creates the account through the
 *        private channel, spends the token, takes the seat, admits the
 *        member and puts them on the task server's team    (the service's half)
 *   11   the person is signed in                                        HERE
 *   12   they land in the product with the team's lists visible          HERE
 *
 * STEP 11 CALLS THE SAME OPERATION THE SIGN-IN PAGE CALLS. `api.signIn` is the
 * only sign-in operation in this product, which is the ticket's "do not build a
 * second way to sign in". And there is NO SIGN-IN FORM HERE: a password form
 * that CREATES the new account is wanted; a second place to sign in with
 * existing credentials is not.
 *
 * EVERY WAY THIS FAILS ENDS ON THE GENERAL ERROR PAGE. An expired token, a
 * withdrawn invitation, a full seat ceiling and an address that already has an
 * account are all refusals the person cannot act on from a form, so they are
 * told what happened and what to do next rather than being left looking at a
 * button that will not work.
 */

'use strict';

import * as api from './api.js';
import {t} from './i18n.js';
import {
  bannerBlock,
  brandBlock,
  esc,
  goToPage,
  installPasswordReveal,
  loadStrings,
  passwordField,
  renderAuth,
  sendToErrorPage,
  showError,
  tx,
} from './auth-shell.js';

/* ------------------------------------------------------------------ *
 * 1. The fragment discipline
 * ------------------------------------------------------------------ */

// Byte-for-byte frontend/src/helpers/signupToken.ts STORAGE_KEY. The two files
// are separate bundles that may not import each other, so there is no shared
// constant and no test that would catch a drift — grep for the literal there
// before renaming it here. Hyphen-free and namespace-free on purpose: it is the
// Vue application's own key, and it must not read like an i18n key to the
// fork-guards sweep.
const SIGNUP_TOKEN_STORAGE_KEY = 'signupToken';
const SIGNUP_TOKEN_FRAGMENT_KEY = 'signup_token';

/* ------------------------------------------------------------------ *
 * 2. Pure parsing
 * ------------------------------------------------------------------ */

/**
 * The invitation handle out of a query string, or null. Only `i` is read; the
 * handle is opaque text belonging to the paid-account service, and this page
 * does not validate it beyond non-emptiness — a wrong handle is that service's
 * refusal to make, not this page's guess.
 */
export function invitationIdFromSearch(search) {
  const value = new URLSearchParams(String(search ?? '').replace(/^\?/, '')).get('i');
  return value === null || value === '' ? null : value;
}

/**
 * The signup token out of a URL fragment, or null. Pure; storage is the
 * caller's.
 *
 * A QUERY-SHAPED STRING IS REFUSED, not parsed: URLSearchParams strips a
 * leading `?` itself, so without this guard a caller handing over
 * `location.search` would find a token there and quietly legitimise moving it
 * into the query — where every access log, proxy log and Referer header sees
 * it. The fragment placement is the security property; the parser enforces it
 * rather than trusting every future caller to.
 */
export function signupTokenFromHash(hash) {
  const raw = String(hash ?? '');
  if (raw.startsWith('?')) return null;
  const fragment = raw.replace(/^#/, '');
  if (fragment === '') return null;
  const token = new URLSearchParams(fragment).get(SIGNUP_TOKEN_FRAGMENT_KEY);
  return token === null || token === '' ? null : token;
}

/**
 * Which refusal to show, as one word the general error page recognises.
 *
 * ONE FUNCTION FOR BOTH ROUTES, because the two vocabularies overlap by design:
 * a withdrawn invitation and an expired one mean the same thing to a reader
 * whether they were discovered while reading the invitation or while completing
 * it, and two tables would be two chances to word them differently.
 *
 * IT FAILS CLOSED. A word this file has not read becomes the general refusal
 * rather than being rendered as its own raw word, so a vocabulary the service
 * grows later tells the reader something true and vague instead of something
 * meaningless and specific.
 *
 * `token_expired` CANNOT HAPPEN IN THE SHIPPED CONFIGURATION: both lifetimes are
 * seven days and the invitation deadline is evaluated first, so
 * `invitation_expired` always wins. It is handled because a configuration change
 * would make it live, and because a value that arrives and is not understood
 * would otherwise become the general sentence, which is the one case where the
 * general sentence is actively wrong — it tells somebody to ask for a new
 * invitation when the invitation is fine and only the link ran out.
 */
export function refusalReason(word) {
  switch (word) {
    case 'invitation_withdrawn': return 'invitation-revoked';
    case 'invitation_expired': return 'invitation-expired';
    case 'token_expired': return 'link-expired';
    case 'at_seat_ceiling': return 'seats-full';
    case 'account_exists': return 'account-exists';
    default: return 'invitation-failed';
  }
}

/**
 * Whether a refusal of the COMPLETION is one the person can act on by changing
 * what they typed, rather than one they must leave the page for.
 *
 * THIS EXISTS BECAUSE ONE ANSWER COVERS TWO DIFFERENT COLLISIONS, and that is
 * deliberate rather than sloppy: an address that already has an account and a
 * username somebody else already holds get the same answer, so an
 * unauthenticated channel cannot be walked to discover who has an account here
 * or what they are called. This page therefore CANNOT tell the two apart, and
 * must not write a sentence that implies it can.
 *
 * NOTHING WAS SPENT, which is what makes staying on the form correct rather than
 * merely kind: the token is still live, so a different username can be submitted
 * immediately. Sending the person to the general error page would end the
 * journey over something they can fix in the field their cursor is already in.
 *
 * THE ADDRESS-ONLY SENTENCE STILL HAS ITS PLACE on the general error page, and
 * it is still correct where it is used: the SUMMARY happens before a username
 * has been chosen, so a collision discovered there can only be the address.
 */
export function recoverableOnTheForm(word) {
  return word === 'account_exists';
}

/* ------------------------------------------------------------------ *
 * 3. The two screens
 * ------------------------------------------------------------------ */

/**
 * THE INVITATION SCREEN: a heading, one sentence, three fields, one button.
 *
 * The body sentence is the ticket's, to be used as written: "You have been
 * invited to join the {teamName} team of {organizationName} for ONE Personal
 * Assistant." It lives in the catalogue with those two placeholders so it can be
 * translated, and both values are escaped because they are names somebody typed
 * into another system.
 *
 * THE ADDRESS IS FILLED IN AND LOCKED, because the token was issued for that
 * address and no other. It is `readonly` rather than `disabled`: a disabled
 * field is out of the tab order and unreadable to some assistive technology, so
 * a person would be told nothing about the one field on the form they cannot
 * change.
 *
 * The empty error box is not a sixth element on the screen — it is `:empty` and
 * therefore invisible, and it exists so a person who presses the button with a
 * field blank is told so rather than watching nothing happen. Every refusal the
 * SERVICE makes leaves this page for the general error page instead.
 */
export function invitationSurface(state) {
  return `${brandBlock()}
    <h1 class="auth-title">${tx('one.join.title')}</h1>
    <p class="auth-lead">${tx('one.join.lead', {
      teamName: state.teamName ?? '',
      organizationName: state.organizationName ?? '',
    })}</p>
    ${bannerBlock()}
    <form class="auth-form" id="joinForm" novalidate>
      <div class="auth-field">
        <label for="email">${tx('one.join.email')}</label>
        <input id="email" name="email" type="email" value="${esc(state.invitedEmail ?? '')}"
          readonly aria-describedby="emailLocked" autocomplete="email">
        <p class="auth-note" id="emailLocked">${tx('one.join.emailLocked')}</p>
      </div>
      <div class="auth-field">
        <label for="username">${tx('one.join.username')}</label>
        <input id="username" name="username" type="text" autocomplete="username"
          autocapitalize="none" spellcheck="false" value="${esc(state.username ?? '')}" required>
      </div>
      ${passwordField('password', 'one.join.password',
        'name="password" autocomplete="new-password" minlength="8" required')}
      <p class="auth-rule">${tx('one.join.passwordRule')}</p>
      <button type="submit" class="auth-submit" id="joinSubmit" ${state.phase === 'working' ? 'disabled' : ''}>
        ${state.phase === 'working' ? tx('one.join.working') : tx('one.join.submit')}
      </button>
    </form>`;
}

/**
 * THE ONE OTHER SCREEN: somebody else is signed in on this browser.
 *
 * This is what an administrator sees when they click an invitation they have
 * just sent, which is the most common arrival of all. Both sentences are the
 * ticket's, to be used as written, and the invitation survives signing out —
 * nothing here consumes it, so pressing the button and coming back lands on the
 * form above.
 */
export function signedInElsewhereSurface() {
  return `${brandBlock()}
    <div class="auth-result">
      <h1 class="auth-title">${tx('one.join.otherAccount.title')}</h1>
      <p>${tx('one.join.otherAccount.body')}</p>
      ${bannerBlock()}
      <button type="button" class="auth-submit" data-action="join-logout">${tx('one.join.otherAccount.logout')}</button>
    </div>`;
}

/**
 * ALREADY A MEMBER, AND THIS IS NOT AN ERROR — criterion 8 and the task server's
 * own rules both require a coherent welcome rather than a refusal. It arrives
 * from both routes: from the summary, when somebody who already holds a seat
 * opens the link, and from the completion, when they press the button anyway.
 *
 * NOTHING WAS SPENT OR CREATED, so the username and password they may have just
 * typed made no account and signing in with them would fail. The only honest
 * next step is the sign-in page and their existing credentials, which is why
 * this screen offers exactly that and nothing else.
 *
 * The organisation is named when the service named it — the summary carries the
 * name and the completion carries only an id — so there are two sentences
 * rather than one with an empty placeholder in it.
 */
export function alreadyMemberSurface(state) {
  const body = state?.organizationName
    ? tx('one.join.alreadyMember.bodyNamed', {organizationName: state.organizationName})
    : tx('one.join.alreadyMember.body');
  return `${brandBlock()}
    <div class="auth-result">
      <h1 class="auth-title">${tx('one.join.alreadyMember.title')}</h1>
      <p>${body}</p>
      <button type="button" class="auth-submit" data-action="join-sign-in">${tx('one.join.alreadyMember.signIn')}</button>
    </div>`;
}

/**
 * THE ACCOUNT AND THE SEAT EXIST AND THE TEAM JOIN DID NOT, which is a partial
 * success and must be said as one. The person can sign in with what they just
 * chose, and they will see nothing shared until an administrator finishes it.
 *
 * THIS IS WHAT EVERY COMPLETION ANSWERS UNTIL THE TASK SERVER IS DEPLOYED, so
 * it is not a rare branch to be worded carelessly — for now it is the common
 * one. Telling somebody "something went wrong" here would be false twice: their
 * account is real, and their seat is real. Landing them silently in an empty
 * product would recreate the exact defect this whole ticket exists to fix,
 * where an invited person sees nothing and nobody tells them why.
 */
export function teamUnavailableSurface() {
  return `${brandBlock()}
    <div class="auth-result">
      <h1 class="auth-title">${tx('one.join.teamUnavailable.title')}</h1>
      <p>${tx('one.join.teamUnavailable.body')}</p>
      <button type="button" class="auth-submit" data-action="join-sign-in">${tx('one.join.teamUnavailable.signIn')}</button>
    </div>`;
}

/** The link carried no handle at all — there is nothing to look up and nothing to join. */
export function missingLinkSurface() {
  return `${brandBlock()}
    <div class="auth-result">
      <h1 class="auth-title">${tx('one.join.missingLink.title')}</h1>
      <p>${tx('one.join.missingLink.body')}</p>
    </div>`;
}

/* ------------------------------------------------------------------ *
 * 4. The impure spine
 * ------------------------------------------------------------------ */

const state = {
  phase: 'reading',
  invitationId: null,
  signupToken: null,
  organizationName: null,
  teamName: null,
  invitedEmail: null,
  username: '',
  // THE VERDICT IS STORED BESIDE THE NAME IT IS ABOUT, never on its own. A
  // person types faster than a network answers, so an answer about `ada` can
  // arrive after they have typed `adamite` — and a bare verdict would then
  // block a name nobody has judged.
  usernameChecked: '',
  usernameVerdict: 'unknown',
};

/**
 * Capture the fragment token for this tab, then strip it from the address bar
 * and history — but ONLY once it is stored: with storage unusable (private
 * modes, policy), the fragment is left in place, because it is the only copy
 * this page has.
 */
function captureSignupToken() {
  const fromHash = signupTokenFromHash(location.hash);
  if (fromHash !== null) {
    try {
      sessionStorage.setItem(SIGNUP_TOKEN_STORAGE_KEY, fromHash);
      history.replaceState(history.state, '', location.pathname + location.search);
    } catch {
      // Storage refused; the token stays in the fragment on purpose.
    }
    return fromHash;
  }
  try {
    const stored = sessionStorage.getItem(SIGNUP_TOKEN_STORAGE_KEY);
    return stored === null || stored === '' ? null : stored;
  } catch {
    return null;
  }
}

function forgetSignupToken() {
  try {
    sessionStorage.removeItem(SIGNUP_TOKEN_STORAGE_KEY);
  } catch {
    // Best effort; it dies with the tab regardless.
  }
}

/**
 * The service's own bounds, mirrored so the page can say something useful
 * instead of sending a request it knows will be refused bodilessly.
 *
 * BYTES, NOT CHARACTERS, and the difference is not pedantry: `password` is
 * bounded at 72 BYTES because that is bcrypt's limit, so a passphrase of
 * twenty-five accented or Japanese characters is over the line while looking
 * comfortably short. Measuring `.length` would let such a password through to a
 * bodiless 400 that this page could only report as "something went wrong".
 */
const PASSWORD_MIN_BYTES = 8;
const PASSWORD_MAX_BYTES = 72;
const USERNAME_MAX_BYTES = 250;

export function byteLength(value) {
  return new TextEncoder().encode(String(value ?? '')).length;
}

/**
 * STEP 7 TO 12. One press, and this is the only thing on the page that writes
 * anything anywhere.
 */
async function submitInvitation(form) {
  const data = new FormData(form);
  const username = String(data.get('username') ?? '').trim();
  const password = String(data.get('password') ?? '');
  state.username = username;

  if (username === '' || password === '') {
    showError(t('one.join.missingFields'));
    return;
  }

  // MANDATORY, AND ONLY ON A DEFINITE ANSWER. The form refuses when the service
  // has said this exact name is taken, or that the task server would refuse it
  // whoever held it. A check still in flight, one that could not run, and one
  // about a name since edited all fall through and submit — blocking on any of
  // those would swallow a press or lock somebody out of joining because their
  // network was slow.
  if (usernameIsBlocked(username, state.usernameChecked, state.usernameVerdict)) {
    // The same sentence the field is already showing. Two wordings for one
    // condition would read as two different problems.
    showError(t(usernameBlockedKey(state.usernameVerdict)));
    document.getElementById('username')?.focus();
    return;
  }
  if (byteLength(username) > USERNAME_MAX_BYTES) {
    showError(t('one.join.usernameTooLong'));
    return;
  }
  const passwordBytes = byteLength(password);
  if (passwordBytes < PASSWORD_MIN_BYTES || passwordBytes > PASSWORD_MAX_BYTES) {
    showError(t('one.join.passwordLength', {min: PASSWORD_MIN_BYTES, max: PASSWORD_MAX_BYTES}));
    return;
  }

  state.phase = 'working';
  render();
  showError(null);

  const result = await api.completeInvitation({
    invitationId: state.invitationId,
    signupToken: state.signupToken,
    username,
    password,
  });

  // EVERY OUTCOME ARRIVES AT HTTP 200, REFUSALS INCLUDED, so `result.ok` alone
  // answers almost nothing here. It is still consulted first, because the two
  // bodiless statuses — a malformed body at 400, and a bodiless refusal — carry
  // no outcome word at all, and reading a word that is not there would send
  // every one of them down the general branch as though the service had spoken.
  const outcome = result.outcome;

  if (result.ok && outcome === 'joined') {
    // The token is spent on the service's side now; dropping our copy stops a
    // stale value being offered by the next flow on a shared machine.
    forgetSignupToken();

    // STEP 11, THROUGH THE PRODUCT'S ONE SIGN-IN OPERATION. A failure here is
    // not a failed acceptance and must not be reported as one: the account, the
    // seat and the team membership all exist, so the person needs the sign-in
    // page rather than the error page.
    try {
      await api.signIn({username, password});
    } catch {
      goToPage('signin');
      return;
    }
    // STEP 12. The settings page is where the lockout lands everybody, and the
    // team's shared lists are visible from there.
    goToPage('settings');
    return;
  }

  if (result.ok && outcome === 'already_member') {
    // NOT A FAILURE, and nothing was spent or created — so the credentials just
    // typed made no account and signing in with them would fail. They are
    // offered the sign-in page and their existing ones instead.
    forgetSignupToken();
    state.phase = 'already-member';
    render();
    return;
  }

  if (outcome === 'team_unavailable') {
    // A PARTIAL SUCCESS, said as one. The account and the seat exist; the team
    // join did not, so they will see nothing shared until an administrator
    // finishes it. Until the task server is deployed this is what EVERY
    // completion answers, so it is the common path rather than a rare one.
    forgetSignupToken();
    state.phase = 'team-unavailable';
    render();
    return;
  }

  if (recoverableOnTheForm(outcome)) {
    // A COLLISION KEEPS THEM HERE. The username they chose may be the whole
    // problem, nothing was spent, and a second attempt costs one word. The
    // sentence names neither collision, because this page cannot tell them
    // apart and must not pretend to.
    state.phase = 'form';
    render();
    // The password survives the re-render, set as a DOM PROPERTY and never as a
    // value attribute in the markup — a password written into innerHTML would
    // be readable in the page source and in every DOM inspection of it. Without
    // this the person retypes a password they got right, to fix a username they
    // did not.
    const field = document.getElementById('password');
    if (field instanceof HTMLInputElement) field.value = password;
    showError(t('one.join.credentialsUnavailable'));
    return;
  }

  // Everything left is a refusal the person cannot act on from this form: a
  // withdrawn invitation, an expired one, a full seat ceiling, an invitation
  // nothing can be said about — which is also the lost race where the token was
  // spent between reading it and spending it. They are told what happened and
  // what to do next on the page that exists for it. A bodiless status carries
  // no word, so `outcome` is null and the general sentence is what they get,
  // which is the honest answer when the service named nothing.
  sendToErrorPage(refusalReason(outcome), result.message);
}

/**
 * Sign the other person out and come back to this same address.
 *
 * `location.reload()` rather than a navigation, so the query keeps the handle
 * and this page re-runs its own boot. The invitation is untouched by any of it.
 */
async function signOutAndReturn() {
  showError(null);
  let providerLogout = null;
  try {
    providerLogout = await api.signOut();
  } catch {
    // The local session is dropped by `signOut` whatever the server answered,
    // so reloading still lands on the invitation form.
  }
  if (providerLogout !== null) {
    location.assign(providerLogout);
    return;
  }
  location.reload();
}

function render() {
  if (state.phase === 'missing-link') {
    renderAuth(missingLinkSurface());
    return;
  }
  if (state.phase === 'signed-in-elsewhere') {
    renderAuth(signedInElsewhereSurface());
    return;
  }
  if (state.phase === 'already-member') {
    renderAuth(alreadyMemberSurface(state));
    return;
  }
  if (state.phase === 'team-unavailable') {
    renderAuth(teamUnavailableSurface());
    return;
  }
  renderAuth(invitationSurface(state));
  const first = document.getElementById('username');
  if (first instanceof HTMLElement && state.phase !== 'working') first.focus();
}

/* ------------------------------------------------------------------ *
 * 4b. Is this username free? — checked while they type
 * ------------------------------------------------------------------ *
 *
 * WHY THIS EXISTS. Somebody whose only problem is that their first choice of
 * username is taken used to be told "this account already exists", sent to an
 * error page, and left looking for an administrator they do not need. Keeping
 * them on the form fixed the dead end; this stops them ever reaching it.
 *
 * THE CHECK IS ADVICE, NEVER AUTHORITY, and every rule below follows from that
 * one sentence. A name can be taken between the check and the press, so the
 * service is still the only thing that decides, and the `account_exists`
 * handling in `submitInvitation` stays exactly as it is. Nothing here may be
 * read as making that branch redundant.
 *
 * THREE THINGS IT MUST NOT DO, each of which would be a worse fault than the
 * one it fixes:
 *
 *   * fight the person mid-word. It waits for a pause rather than firing on
 *     every keystroke, and it updates the page IN PLACE rather than
 *     re-rendering — a re-render moves the caret to the end of the field and
 *     makes editing the middle of a name impossible;
 *   * swallow a press. A check still in flight never blocks submission; the
 *     form goes to the service and the service decides;
 *   * lock somebody out when it cannot run. Offline, timed out, refused, and a
 *     body this page did not recognise all answer `unknown`, and `unknown`
 *     always allows.
 *
 * TWO VERDICTS BLOCK AND THEY ARE NOT THE SAME NEWS. `taken` means somebody
 * else holds the name. `invalid` means the task server would refuse that string
 * whoever held it, so its sentence must not imply anybody has it. Both are the
 * service answering; the difference from `unknown` is knowing, not severity.
 */

// Long enough that a person typing a name never sees a message about a prefix
// of it, short enough that a pause before pressing the button is usually
// answered first.
const USERNAME_CHECK_DELAY_MS = 450;

let usernameCheckTimer = null;
// Only the newest request may write a verdict. Without this, a slow answer
// about an earlier name overwrites a fast answer about the current one, and the
// form blocks or allows on a name the person has already replaced.
let usernameCheckSequence = 0;

/**
 * The sentence a verdict earns, or null when it earns none.
 *
 * ONE PLACE DECIDES BOTH WHETHER THE FORM BLOCKS AND WHAT IT SAYS, because the
 * two answers must never disagree: a disabled button with no sentence is a form
 * that has stopped working for no stated reason, and a sentence with a live
 * button is advice the person can ignore into a refusal.
 *
 * THE TWO SENTENCES MUST NOT SAY EACH OTHER'S THING. `taken` means somebody
 * else holds it, and the person needs to know a different name will work.
 * `invalid` means the task server would refuse that string whoever held it, so
 * it must NOT suggest anybody has it — that would send somebody hunting for a
 * collision that does not exist, and it would leak a claim about another
 * account that is not true.
 */
export function usernameBlockedKey(verdict) {
  if (verdict === 'taken') return 'one.join.usernameTaken';
  if (verdict === 'invalid') return 'one.join.usernameUnusable';
  return null;
}

/**
 * Whether the form should refuse to submit right now.
 *
 * PURE, so the whole rule is a table rather than a browser state. It blocks on
 * a DEFINITE answer about the name currently in the field, and on nothing else.
 *
 * THE LINE IS "DOES THE SERVICE KNOW", not "is the news bad". `taken` and
 * `invalid` are both the service answering, so both block. Not yet checked,
 * still in flight, could not be checked, checked and free, and a verdict about
 * a name the person has since edited are all NOT KNOWING, and every one of them
 * allows — a person on a bad network can still submit and let the service
 * decide, which is where the fail-open property belongs.
 */
export function usernameIsBlocked(current, checkedName, verdict) {
  return usernameBlockedKey(verdict) !== null
    && String(current ?? '') === String(checkedName ?? '');
}

/**
 * Show the verdict WITHOUT re-rendering.
 *
 * `render()` replaces the card's innerHTML, which destroys the field the person
 * is typing in and puts their caret at the end of it. This writes the two things
 * that change — the sentence and whether the button is usable — straight onto
 * the nodes that carry them.
 */
function applyUsernameVerdict() {
  const button = document.getElementById('joinSubmit');
  const blocked = usernameIsBlocked(state.username, state.usernameChecked, state.usernameVerdict);
  if (button instanceof HTMLButtonElement && state.phase !== 'working') button.disabled = blocked;
  const key = blocked ? usernameBlockedKey(state.usernameVerdict) : null;
  showError(key === null ? null : t(key));
}

/**
 * Ask, once the typing has paused.
 *
 * The verdict is cleared the moment the name changes, so a person who edits a
 * blocked name is never left with a disabled button and a stale sentence about
 * a name they no longer have.
 */
function scheduleUsernameCheck(value) {
  const username = String(value ?? '').trim();
  state.username = username;
  state.usernameChecked = '';
  state.usernameVerdict = 'unknown';
  applyUsernameVerdict();

  if (usernameCheckTimer !== null) clearTimeout(usernameCheckTimer);
  if (username === '') return;

  const sequence = ++usernameCheckSequence;
  usernameCheckTimer = setTimeout(async () => {
    let verdict = 'unknown';
    try {
      verdict = await api.checkInvitationUsername({
        invitationId: state.invitationId,
        signupToken: state.signupToken,
        username,
      });
    } catch {
      // Offline, refused, or a shape this page did not recognise. `unknown`
      // allows, and the service decides at submission.
      verdict = 'unknown';
    }
    // A newer keystroke has already asked a newer question; this answer is
    // about a name that is no longer on the form.
    if (sequence !== usernameCheckSequence) return;
    state.usernameChecked = username;
    state.usernameVerdict = verdict === 'taken' || verdict === 'invalid' || verdict === 'free'
      ? verdict
      : 'unknown';
    applyUsernameVerdict();
  }, USERNAME_CHECK_DELAY_MS);
}

function installListeners() {
  // `input` rather than `keyup`, so a paste, a drag, an autofill and a
  // speech-to-text insertion are all seen — a check that only watched keys
  // would let three of those four past unchecked.
  // One delegated listener drives every reveal control on this page; it is installed
  // once and survives every re-render.
  installPasswordReveal();

  document.addEventListener('input', (event) => {
    const el = event.target;
    if (!(el instanceof HTMLInputElement) || el.id !== 'username') return;
    scheduleUsernameCheck(el.value);
  });

  document.addEventListener('submit', (event) => {
    if (!(event.target instanceof HTMLFormElement) || event.target.id !== 'joinForm') return;
    event.preventDefault();
    submitInvitation(event.target);
  });
  document.addEventListener('click', (event) => {
    const el = event.target instanceof Element ? event.target.closest('[data-action]') : null;
    if (el === null) return;
    const action = el.getAttribute('data-action');
    if (action === 'join-logout') {
      event.preventDefault();
      signOutAndReturn();
      return;
    }
    if (action === 'join-sign-in') {
      event.preventDefault();
      goToPage('signin');
    }
  });
}

export async function boot() {
  if (typeof document === 'undefined') return;

  installListeners();
  await loadStrings();

  state.signupToken = captureSignupToken();
  state.invitationId = invitationIdFromSearch(location.search);

  if (state.invitationId === null) {
    state.phase = 'missing-link';
    render();
    return;
  }

  // A MANGLED LINK IS CAUGHT BEFORE THE REQUEST, not after. Both routes answer a
  // bodiless 400 for a malformed body, which arrives here indistinguishable from
  // a bug in this page — so a link truncated by a mail client would produce a
  // blank refusal instead of "open the link from your email again".
  if (!api.invitationCredentialsAreWellFormed(state.invitationId, state.signupToken)) {
    sendToErrorPage('invitation-unknown');
    return;
  }

  // STEP 3: read whether anybody is signed in, and do nothing else with the
  // answer. A session belonging to somebody else is the administrator case, and
  // it gets its own screen rather than a silent failure.
  if (await api.initSession()) {
    state.phase = 'signed-in-elsewhere';
    render();
    return;
  }

  // STEP 4: what organisation and team does this handle name? A read, with no
  // session, whose credential is the token. Nothing is consumed.
  const summary = await api.readInvitationSummary({
    invitationId: state.invitationId,
    signupToken: state.signupToken,
  });

  // A BODILESS ANSWER MEANS THE CALLER PROVED NOTHING — an unknown handle, or a
  // token that is unknown, unbound, or minted for a different invitation. Those
  // are one answer on purpose, so handles appearing in an access log cannot be
  // sorted into live and dead, and this page must not try to guess which it was.
  // The advice that fits all of them is the same: open the link from the email
  // again, or ask for a new one.
  if (!summary.ok) {
    sendToErrorPage('invitation-unknown');
    return;
  }

  const details = api.readInvitationSummaryBody(summary);
  state.organizationName = details.organizationName;

  // THE STATE IS THE VERDICT AND `ok` IS NOT. All five states arrive at HTTP
  // 200, including the three a person cannot act on, so a page that read `ok`
  // alone would show the invitation form to somebody whose invitation was
  // withdrawn.
  if (details.state === 'already_member') {
    // Not an error in either direction. They hold a seat already, so the way in
    // is the sign-in page and the credentials they already have.
    state.phase = 'already-member';
    render();
    return;
  }

  if (details.state !== 'usable') {
    // `invitation_withdrawn`, `invitation_expired`, `token_expired`, and any
    // state a later change adds that this page has not read. Failing closed here
    // is what stops a new state opening the form to somebody it was invented to
    // keep out.
    sendToErrorPage(refusalReason(details.state));
    return;
  }

  state.teamName = details.teamName;
  state.invitedEmail = details.invitedEmail;
  state.phase = 'form';
  render();
}

/* Boot only on the real page. A test importing the pure functions has no
 * `#auth` in its document and gets no fetch, no storage and no render. */
if (typeof document !== 'undefined' && document.getElementById('auth') !== null) {
  queueMicrotask(() => {boot();});
}
