# KMonad Key Counter

Tracks frequencies for keys by parsing [KMonad](https://github.com/kmonad/kmonad)'s debug log output.
Also attempts to provide timeseries data while taking effort not to leak details about passwords.
Much of this README describes security concerns regarding passwords and other sensitive material, and mitigations I've taken to deal with them.

## Motivation: Limited number of O Rings

I bought a pack of [O-Rings](https://www.amazon.com/Glorious-Mechanical-Keyboard-Ring-G40-THIN/dp/B073QXKX2P) off Amazon to make my mechanical keyboard less noisy.
After stacking two rings on most of my keys (which yielded the best results for noise-level), I realized I didn't have enough rings to put two on every key; thus, I would have to make tradeoffs between keys.
Of course, I could've just bought more rings, but this seemed like a fun problem to solve.

There were a few obvious sacrifices I could make, like removing both rings from the `menu` key, which I never use, as well as from `rctrl` and `fn`, which rarely use.
However, outside of these obvious cases, it was difficult to reason about things like: do I use `0` or `z` more often?
Since I use Vim and generally have lots of untraditional keybindings, this comparison was infeasible to make without data.

I was already using KMonad, so tracking key frequencies using KMonad's log output was a natural approach.

## Security

A simple approach to key counting would be to have a user process parse KMonad's log output, incrementing the value in `~/.local/share/kmonad-key-counter/<key>` with each subsequent `key` press.
The issue with this approach is that any unprivileged user process would then have complete access to the stream of keystrokes in real time by continuously `diff`'ing the frequencies.
This requires that we run the key counter process as `root` or some dedicated user, and write key frequencies to a non-user-readable location.
For simplicity, I personally run it as `root`, but for the security-conscious I would recommend either going through the code in [`main.go`](./main.go) or running it as a dedicated user after granting the user the necessary permissions for the cache file, FIFO and destination directory.

Now, we've established that the key counting process runs under a separate user to prevent keystroke monitoring.
Ideally, regular users would have access to some sort of information regarding key counts, aggregated in a way that strips out any sensitive information.
We can't simply expose key counts aggregated at an hourly/daily basis, because it's possible that the user logs into their machine using their password at the tail of the time bucket, which would cause the frequency data to contain only the characters from their password; similarly, we can't just keep track of _rolling_ 1 {hour,day} windows because it's possible that the user logs in using their password and then doesn't type at all for the rest of the time window.

My approach to this problem was **count-wise bucketing**, where each bucket is also called a "window".
When running the key counter process, you may specify a `MaxKeypressesPerWindow` config parameter, set to 100k by default, which means that the key counter will hold 100k total keypresses in cache before writing out the key counts to a user-readable location.
I type a bit over 100 WPM, and empirically I seem to hit 100k keystrokes in around 5/6 days; this means that I end up with discrete time windows counting key frequencies over 5-6 day periods.
When writing the counts, it also includes the timestamps of the first and last keystroke in the window to enable time-series processing.

One unfortunate implementation detail is that in order to avoid throwing away partial buckets between restarts, we have to maintain a partial bucket cache.
This cache location is configurable via the `CacheFilePath` config parameter; to prevent keystroke monitoring attacks, the file should be readable only by `root`/the dedicated user.

The process writes to the cache at a frequency of `CacheWriteFrequency`, which is 30 seconds by default.
Theoretically, this could be phased out in favor of writing only on shutdown/SIGTERM, which would mitigate (though not eliminate) the risk of monitoring attacks via the cache file.
However, if this seems important to you, keep in mind that if `CacheFilePath` is accessible solely by `root`/a dedicated user, this would only matter in cases where a bad actor has `root`-like access to the system; in this case, the system has been fully compromised anyway and they could just as easily take control of your keyboard on their own, with or without you knowing.

### Theoretical Attack Vector

This hack would be quite difficult to pull off in real life, but is still interesting to think about and important to keep in mind.
Imagine the following scenario.

The user, Alice (she/her), does this:
1. logs into machine with password
2. works on an essay (some long-form content) for the rest of the day, doing nothing else on their computer; at the end of the day, she hits the `MaxKeypressesPerWindow` and the frequency count is written to a user-readable location

Then, a malicious actor Bob (he/him) with access to the newly-written frequency data and the final essay text does this:
1. read the output frequency data
2. read the frequency data of the characters in the essay
3. subtract the essay's frequency data from the overall frequency data

Now, despite the overall frequency data initially being rich, which masked the password's character frequencies, the malicious actor Bob has taken out all of this variance, leaving only the password.

This scenario is extraordinarily generous for Bob and is definitely a **worst case scenario**; on top of the unusual level of access granted to him we also assumed that Alice used _no keybindings_ and also _never deleted any text_, since both of these actions would result in discrepancies between the final essay text and the raw character frequency counts.

However, we can imagine a similar but far more likely scenario where Alice's actions on the computer can be tracked to a reasonably granular level for everything *but* the passwords.
If she were screensharing on Zoom or some equivalent platform, it would be fairly straightforward for a participant in the call to keep track of Alice's keypresses within an acceptable margin of error, just from what they can see on screen.
Keybindings make this sort of tracking more difficult, but still not impossible.
If the participant sees a "Search" menu show up on screen and Alice did not use her mouse to open it, they could deduce that she used Ctrl+F; then, they would increment the F count by 1, on top of whatever they had already seen.

One way to mitigate this attack in our system is to simply crank up the `MaxKeypressesPerWindow` configuration variable until it's infeasible to track all of the user's actions over the course of the window.
The default of 100k should prevent any of these attacks, but depending on your typing speed and desired time-series granularity, you may want to tune this.

Also note that this attack vector is possible only if other parties have access to your window count data, which for most people would be undesirable in the first place.
Generally speaking, you should keep this data private unless you can prove to yourself that it cannot reasonably be exploited.
