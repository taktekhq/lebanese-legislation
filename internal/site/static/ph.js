/* PostHog: project LebaneseLegislation (EU cloud). A file, not an inline
   script, so the Content Security Policy needs no extra hash. Everything
   goes through /ph on our own domain (the reverse proxy sends it to PostHog), so ad
   blockers don't hide visitors and the CSP stays 'self'.

   Page views, page leaves, clicks (autocapture), heatmaps, dead clicks, web
   vitals and JavaScript errors are captured; session replay records the
   share of visits set in the project (25%, longer than 10 seconds). Named
   events are sent by ga.js's track(). PostHog keeps everything, so inputs
   are not masked (the site has no passwords). Loaded unless the server
   runs with ANALYTICS=off. */
!function(t,e){var o,n,p,r;e.__SV||(window.posthog=e,e._i=[],e.init=function(i,s,a){function g(t,e){var o=e.split(".");2==o.length&&(t=t[o[0]],e=o[1]),t[e]=function(){t.push([e].concat(Array.prototype.slice.call(arguments,0)))}}(p=t.createElement("script")).type="text/javascript",p.crossOrigin="anonymous",p.async=!0,p.src=s.api_host.replace(".i.posthog.com","-assets.i.posthog.com")+"/static/array.js",(r=t.getElementsByTagName("script")[0]).parentNode.insertBefore(p,r);var u=e;for(void 0!==a?u=e[a]=[]:a="posthog",u.people=u.people||[],u.toString=function(t){var e="posthog";return"posthog"!==a&&(e+="."+a),t||(e+=" (stub)"),e},u.people.toString=function(){return u.toString(1)+".people (stub)"},o="init capture register register_once register_for_session unregister unregister_for_session getFeatureFlag getFeatureFlagPayload isFeatureEnabled reloadFeatureFlags on onFeatureFlags onSessionId identify setPersonProperties group resetGroups reset get_distinct_id getGroups get_session_id get_session_replay_url alias set_config startSessionRecording stopSessionRecording sessionRecordingStarted captureException loadToolbar get_property getSessionProperty createPersonProfile opt_in_capturing opt_out_capturing has_opted_in_capturing has_opted_out_capturing clear_opt_in_out_capturing debug".split(" "),n=0;n<o.length;n++)g(u,o[n]);e._i.push([i,s,a])},e.__SV=1)}(document,window.posthog||[]);

/* Session replay copies the whole page when it starts: on a long law (the
   penal code has 5,600 elements) that froze the browser for 1.4 s
   (Lighthouse, 4 Nov 2026). Long pages are not recorded, except for a paid
   student editor (legislation.js keeps their code from their ?editor= link in
   localStorage, so later visits count too): every editor session is
   recorded, whatever the project's sampling, the visitor is identified as
   editor:CODE so all their sessions sit under one person, and every event
   carries the code (Nizar, 5 Nov 2026: "No replay on long pages ... replay
   for editors only", "These sessions need to be recorded by posthog, and
   logged by posthog. All of them."). */
(function () {
  var editor = null;
  try {
    editor = localStorage.getItem("lebleg_editor");
  } catch (e) {}
  var long = document.getElementsByTagName("*").length > 1500;
  posthog.init("phc_pTpSY5wVsQrG8LN58z4zWXdMWjvqR6uPCY2eXV25sBV5", {
    api_host: "/ph",
    ui_host: "https://eu.posthog.com",
    defaults: "2025-05-24",
    person_profiles: "identified_only",
    capture_pageview: true,
    capture_pageleave: true,
    disable_session_recording: long && !editor,
    session_recording: { maskAllInputs: false, maskInputOptions: { password: true } }
  });
  if (!editor) return;
  posthog.register({ editor: editor });
  posthog.identify("editor:" + editor, { editor: editor });
  posthog.startSessionRecording(true);
})();
