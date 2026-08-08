"use client";

import { motion } from "framer-motion";
import Image from "next/image";
import { ArrowRight, Play, Users, Clapperboard, Globe2, Sparkles, TrendingUp } from "lucide-react";

interface HeroSectionProps {
  onOpenWaitlist: () => void;
}

export default function HeroSection({ onOpenWaitlist }: HeroSectionProps) {
  return (
    <section className="relative pt-32 pb-20 md:pt-40 md:pb-32 overflow-hidden bg-grid-pattern border-b border-slate-200/60">
      {/* Decorative Light Gradient Spotlights */}
      <div className="absolute top-1/4 left-1/2 -translate-x-1/2 -translate-y-1/2 w-[600px] h-[350px] bg-gradient-to-tr from-amber-200/30 via-slate-200/40 to-sky-200/20 blur-3xl rounded-full pointer-events-none" />

      <div className="max-w-6xl mx-auto px-4 sm:px-6 relative z-10">
        <div className="grid grid-cols-1 lg:grid-cols-12 gap-12 lg:gap-8 items-center">
          
          {/* Left Column: Text & CTAs */}
          <div className="lg:col-span-7 space-y-6 sm:space-y-8 text-left">
            
            {/* Editorial Mono Tag */}
            <motion.div
              initial={{ opacity: 0, y: 15 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ duration: 0.5 }}
              className="inline-flex items-center gap-2 px-3.5 py-1.5 rounded-full bg-white border border-slate-200/80 shadow-sm text-xs font-mono text-slate-700"
            >
              <span className="w-2 h-2 rounded-full bg-amber-500 animate-pulse" />
              <span>— ENTERTAINMENT CAPITAL MARKETPLACE</span>
            </motion.div>

            {/* Main Headline */}
            <motion.h1
              initial={{ opacity: 0, y: 20 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ duration: 0.6, delay: 0.1 }}
              className="text-4xl sm:text-5xl md:text-6xl font-extrabold tracking-tight text-slate-900 leading-[1.1]"
            >
              The Future of Entertainment Funding{" "}
              <span className="relative inline-block text-transparent bg-clip-text bg-gradient-to-r from-slate-900 via-amber-800 to-slate-900">
                Starts Here.
              </span>
            </motion.h1>

            {/* Subheadline */}
            <motion.p
              initial={{ opacity: 0, y: 20 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ duration: 0.6, delay: 0.2 }}
              className="text-lg sm:text-xl text-slate-600 font-normal leading-relaxed max-w-2xl"
            >
              Big Film Fund is building a new way to finance entertainment — connecting visionary creators with a global community of people who want to discover, support, and participate in the next generation of films.
            </motion.p>

            {/* CTAs */}
            <motion.div
              initial={{ opacity: 0, y: 20 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ duration: 0.6, delay: 0.3 }}
              className="flex flex-col sm:flex-row items-stretch sm:items-center gap-4 pt-2"
            >
              <button
                onClick={onOpenWaitlist}
                className="px-7 py-4 rounded-xl bg-slate-900 text-white font-semibold text-base hover:bg-slate-800 transition-all shadow-xl shadow-slate-900/15 hover:shadow-2xl hover:shadow-slate-900/25 flex items-center justify-center gap-2 group"
              >
                <span>Join the Waitlist</span>
                <ArrowRight className="w-4 h-4 group-hover:translate-x-1 transition-transform" />
              </button>

              <a
                href="#how-it-works"
                className="px-7 py-4 rounded-xl bg-white text-slate-800 font-semibold text-base border border-slate-200 hover:bg-slate-50 transition-all shadow-sm flex items-center justify-center gap-2.5"
              >
                <div className="w-6 h-6 rounded-full bg-slate-100 flex items-center justify-center text-slate-700">
                  <Play className="w-3 h-3 fill-slate-700 ml-0.5" />
                </div>
                <span>Explore How It Works</span>
              </a>
            </motion.div>

            {/* Trust / Early Access Badges */}
            <motion.div
              initial={{ opacity: 0 }}
              animate={{ opacity: 1 }}
              transition={{ duration: 0.6, delay: 0.4 }}
              className="pt-4 flex flex-wrap items-center gap-6 text-xs text-slate-500 font-mono border-t border-slate-200/60"
            >
              <div className="flex items-center gap-2">
                <Users className="w-4 h-4 text-slate-700" />
                <span>Global Audience Network</span>
              </div>
              <div className="flex items-center gap-2">
                <Clapperboard className="w-4 h-4 text-amber-600" />
                <span>Visionary Filmmakers</span>
              </div>
              <div className="flex items-center gap-2">
                <Globe2 className="w-4 h-4 text-sky-600" />
                <span>Direct Participation</span>
              </div>
            </motion.div>
          </div>

          {/* Right Column: Cinematic Frame Asset & Floating Metrics */}
          <motion.div
            initial={{ opacity: 0, scale: 0.96, y: 20 }}
            animate={{ opacity: 1, scale: 1, y: 0 }}
            transition={{ duration: 0.7, delay: 0.2 }}
            className="lg:col-span-5 relative"
          >
            <div className="relative rounded-2xl overflow-hidden border border-slate-200 bg-white p-2 shadow-2xl shadow-slate-900/10">
              
              {/* Main Image Frame */}
              <div className="relative rounded-xl overflow-hidden aspect-[4/3] bg-slate-100">
                <Image
                  src="/images/hero-cinematic.png"
                  alt="Big Film Fund — Cinematic Entertainment Capital Studio"
                  fill
                  priority
                  className="object-cover"
                />
                
                {/* Cinematic Vignette Overlay */}
                <div className="absolute inset-0 bg-gradient-to-t from-slate-950/70 via-transparent to-transparent" />

                {/* Tag Overlay on Image */}
                <div className="absolute top-4 left-4 flex items-center gap-2 px-3 py-1 rounded-md bg-slate-900/80 backdrop-blur-md text-white text-[11px] font-mono tracking-wider">
                  <Sparkles className="w-3 h-3 text-amber-400" />
                  <span>EARLY FOUNDER ACCESS</span>
                </div>

                {/* Subtitle Overlay on Image */}
                <div className="absolute bottom-4 left-4 right-4 text-white space-y-1">
                  <div className="text-xs font-mono tracking-widest text-slate-300 uppercase">
                    Platform Ecosystem
                  </div>
                  <div className="text-sm font-semibold tracking-tight text-white">
                    Connecting Global Capital & Creative Production
                  </div>
                </div>
              </div>

              {/* Floating Stat Card (Bottom Left Overlay) */}
              <div className="hidden sm:flex absolute -bottom-6 -left-6 items-center gap-3 bg-white/95 backdrop-blur-md p-4 rounded-xl border border-slate-200 shadow-xl max-w-xs">
                <div className="w-10 h-10 rounded-lg bg-amber-100 flex items-center justify-center text-amber-700 shrink-0">
                  <TrendingUp className="w-5 h-5" />
                </div>
                <div>
                  <div className="text-xs font-mono text-slate-500 uppercase">Community Interest</div>
                  <div className="text-base font-bold text-slate-900">$14.2M+ Allocated</div>
                </div>
              </div>

              {/* Floating Feeling Callout Badge */}
              <div className="hidden sm:flex absolute -top-5 -right-5 items-center gap-2 bg-slate-900 text-white px-4 py-2.5 rounded-full text-xs font-medium shadow-xl border border-slate-700">
                <span className="w-2 h-2 rounded-full bg-emerald-400 animate-pulse" />
                <span>"I am early to something important."</span>
              </div>
            </div>
          </motion.div>

        </div>
      </div>
    </section>
  );
}
