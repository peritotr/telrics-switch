// SPDX-License-Identifier: AGPL-3.0-only
import '@fontsource/ibm-plex-sans/400.css'
import '@fontsource/ibm-plex-sans/500.css'
import '@fontsource/ibm-plex-sans/600.css'
import '@fontsource/ibm-plex-mono/400.css'
import '@fontsource/ibm-plex-mono/500.css'
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { createPanel } from './panel'
import './ui/ui.css'

const Panel = createPanel()

// One root per page, also when a code change re-runs this module in development.
const holder = window as unknown as { __panelRoot?: ReturnType<typeof createRoot> }
holder.__panelRoot ??= createRoot(document.getElementById('root')!)
holder.__panelRoot.render(<StrictMode><Panel /></StrictMode>)
