# Your first session on a station

Someone has given you an account on a Vibium Station and shared a phone
with your team. This page takes you from the setup link to a first
"hello": a screenshot and one tap. You do it twice, first in your browser
and then from Vibium, which is how a script or an agent drives the phone.

The example is a new phone that still shows its welcome screen. Any
screen works.

## What you need

- A setup link from the station's admin. It works once, for 24 hours.
- An authenticator app, such as the one in your phone's password manager.
- For Vibium, in step 4: Node.js 18 or newer.

## 1. Make your account

1. Open the setup link.
2. Choose a password of at least 12 characters.
3. Scan the QR code with your authenticator app and enter the six-digit
   code it shows.
4. Keep the ten backup codes. The station shows them once, and each one
   signs you in once when the app is out of reach.

After that you sign in at the station's address with your login, your
password and a code from the app.

## 2. Watch or drive

**Devices** lists what your teams were given. Each device has two ways in:

- **Watch**, the eye, only looks. It sends no input, so it is safe while
  someone else is using the phone.
- **Drive**, the wheel, takes the phone. Your mouse and keyboard now
  control it.

One person or agent drives at a time. The **Status** column says who has
the phone, or "available".

What you may do depends on the level the admin gave your team:

| Level | You may |
|---|---|
| watch | Watch the phone |
| drive | Also drive it in your browser |
| jam | Also drive it from Vibium, with a script or an agent, and start jam sessions |

A jam session is a chat on the station's own pages: you type a prompt, and
an AI chat model carries it out on the phone. A station has them only when
its admin has set up a model.

## 3. Hello, in your browser

1. On **Devices**, press **Drive** next to the phone. Its screen appears.
2. Press **Save a screenshot** in the toolbar. Your browser saves a picture
   of the screen.
3. Click something on the phone. A click is a tap, and a drag is a swipe.
   On a welcome screen, click the arrow or button that starts setup.
4. On an Android phone, press **Back** in the toolbar to return to the
   screen before.
5. Press **Release the device** when you are done. You are back on
   **Devices**, and the phone is free for the next person.

If the picture is black, the phone's screen is off. Press **Home** in the
toolbar to wake it.

A phone you leave alone for five minutes is released by itself.

## 4. Hello, from Vibium

This needs the **jam** level on the phone.

1. On your **Account** page, under **API tokens**, type a name, leave the
   level at **drive** and press **Create token**. Copy the token, which
   starts with `stk_`. The station shows it once.
2. Install [Vibium](https://github.com/VibiumDev/vibium):

   ```bash
   npm install -g vibium
   ```

3. Tell Vibium where the station is, with your station's address in place
   of `station.example`:

   ```bash
   export VIBIUM_CONNECT_URL=wss://station.example/session
   export VIBIUM_CONNECT_API_KEY=stk_...
   ```

4. Take a screenshot, tap, and go back:

   ```bash
   vibium start                     # takes the phone
   vibium screenshot -o hello.jpg
   vibium tap X Y                   # where to tap, in pixels of hello.jpg
   vibium screenshot -o next.jpg
   vibium keys Escape               # Back, on an Android phone
   vibium stop                      # gives the phone back
   ```

   Open `hello.jpg`, find the arrow or button, and put its position in
   place of `X Y`. `next.jpg` shows the screen that follows.

If `vibium start` answers `no device is online`, the phone is offline or
your team does not have **jam** on it. Ask the admin.

While Vibium has the phone, **Devices** shows your token's name as the
holder. To take the phone back in your browser, open it and press **Hold
the device** in the toolbar. That ends the Vibium session.

## Where next

- [Reach a device remotely through the station](agent-through-the-station.md):
  holds, waiting in line, and tokens.
- [Automate Roadie with Vibium](https://github.com/VibiumDev/roadie/blob/main/docs/tutorials/automate-with-vibium.md):
  keys, typing, recordings, and the same from a script or an agent.
