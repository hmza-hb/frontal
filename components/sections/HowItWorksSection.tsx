"use client";

import { useState } from "react";
import { motion, AnimatePresence } from "framer-motion";
import Image from "next/image";
import { Compass, HeartHandshake, Film, ChevronRight, CheckCircle, ArrowRight, Play, Star } from "lucide-react";

interface HowItWorksSectionProps {
  onOpenWaitlist: () => void;
}

export default function HowItWorksSection({ onOpenWaitlist }: HowItWorksSectionProps) {
  const [activeStep, setActiveStep] = useState(0);

  const steps = [
    {
      id: 0,
      stepNum: "STEP 1",
      title: "Discover",
      description:
        "Explore upcoming film projects selected for their vision, potential, and creative impact.",
      icon: Compass,
      detail:
        "Browse transparent project bibles, director reels, budget breakdowns, and commercial distribution targets for vetted film projects.",
      metric: "Curated Selection Rate: Top 3%",
    },
    {
      id: 1,
      stepNum: "STEP 2",
      title: "Participate",
      description:
        "Join a community of supporters helping bring these stories to life.",
      icon: HeartHandshake,
      detail:
        "Directly allocate support to projects you believe in. Gain early access tiers, special credits, and community governance perks.",
      metric: "Fractional Participation from $100",
    },
    {
      id: 2,
      stepNum: "STEP 3",
      title: "Follow the Journey",
      description:
        "Stay connected as projects progress from concept to production and beyond.",
      icon: Film,
      detail:
        "Receive behind-the-scenes production logs, table-read streams, premiere invites, and distribution revenue distribution updates.",
      metric: "Real-time Production Milestones",
    },
  ];

  return (
    <section id="how-it-works" className="py-24 md:py-32 bg-slate-50 border-b border-slate-200/60 relative">
      <div className="max-w-6xl mx-auto px-4 sm:px-6">
        
        {/* Section Header */}
        <div className="max-w-3xl space-y-4 mb-16">
          <div className="inline-flex items-center gap-2 px-3 py-1 rounded-md bg-sky-100 text-sky-800 font-mono text-xs uppercase tracking-wider">
            <Compass className="w-3.5 h-3.5" />
            <span>— SECTION 4: HOW IT WORKS</span>
          </div>

          <h2 className="text-3xl sm:text-4xl md:text-5xl font-extrabold tracking-tight text-slate-900 leading-tight">
            A New Way to Participate in Entertainment.
          </h2>

          <p className="text-lg text-slate-600 font-normal leading-relaxed">
            Three simple steps connecting film creators and global supporters in a transparent, streamlined ecosystem.
          </p>
        </div>

        {/* Step Tabs & Live Interactive Project Preview Grid */}
        <div className="grid grid-cols-1 lg:grid-cols-12 gap-8 items-start">
          
          {/* Left Column: Interactive Step Selector List */}
          <div className="lg:col-span-6 space-y-4">
            {steps.map((step, idx) => {
              const Icon = step.icon;
              const isSelected = activeStep === idx;
              return (
                <div
                  key={step.title}
                  onClick={() => setActiveStep(idx)}
                  className={`cursor-pointer p-6 rounded-2xl border transition-all duration-300 ${
                    isSelected
                      ? "bg-white border-slate-900 shadow-xl ring-1 ring-slate-900"
                      : "bg-white/60 border-slate-200/80 hover:bg-white hover:border-slate-300"
                  }`}
                >
                  <div className="flex items-start gap-4">
                    <div
                      className={`w-10 h-10 rounded-xl flex items-center justify-center shrink-0 transition-colors ${
                        isSelected
                          ? "bg-slate-900 text-white"
                          : "bg-slate-100 text-slate-600"
                      }`}
                    >
                      <Icon className="w-5 h-5" />
                    </div>

                    <div className="flex-1 space-y-1">
                      <div className="flex items-center justify-between">
                        <span className="font-mono text-xs font-bold text-amber-700">
                          {step.stepNum}
                        </span>
                        {isSelected && (
                          <span className="text-[11px] font-mono px-2 py-0.5 rounded bg-emerald-100 text-emerald-800">
                            Active Interactive Preview
                          </span>
                        )}
                      </div>

                      <h3 className="text-xl font-bold text-slate-900">
                        {step.title}
                      </h3>

                      <p className="text-sm text-slate-600 leading-relaxed pt-1">
                        {step.description}
                      </p>

                      {isSelected && (
                        <motion.div
                          initial={{ opacity: 0, height: 0 }}
                          animate={{ opacity: 1, height: "auto" }}
                          className="pt-3 space-y-3 text-xs text-slate-500 border-t border-slate-100 mt-3"
                        >
                          <p className="text-slate-700 font-medium">{step.detail}</p>
                          <div className="inline-block font-mono text-[11px] px-2.5 py-1 rounded bg-slate-100 text-slate-800">
                            {step.metric}
                          </div>
                        </motion.div>
                      )}
                    </div>
                  </div>
                </div>
              );
            })}
          </div>

          {/* Right Column: Live Interactive Film Project Card Teaser */}
          <div className="lg:col-span-6 sticky top-28">
            <div className="bg-white rounded-2xl border border-slate-200 p-6 shadow-2xl space-y-6">
              
              <div className="flex items-center justify-between border-b border-slate-100 pb-4">
                <div className="flex items-center gap-2 text-xs font-mono text-slate-500">
                  <span className="w-2.5 h-2.5 rounded-full bg-emerald-500 animate-pulse" />
                  <span>FEATURED CAMPAIGN PREVIEW</span>
                </div>
                <span className="text-xs font-mono text-slate-900 font-semibold">
                  STATUS: PRE-FUNDING
                </span>
              </div>

              {/* Sample Film Poster Frame */}
              <div className="relative rounded-xl overflow-hidden aspect-[16/10] bg-slate-900 group">
                <Image
                  src="/images/project-preview.png"
                  alt="Featured Film Teaser — The Horizon Line"
                  fill
                  className="object-cover group-hover:scale-105 transition-transform duration-500"
                />
                
                <div className="absolute inset-0 bg-gradient-to-t from-slate-950 via-slate-950/30 to-transparent" />

                <div className="absolute top-3 right-3 bg-slate-900/80 backdrop-blur-md px-3 py-1 rounded-full text-white text-[11px] font-mono flex items-center gap-1.5">
                  <Star className="w-3 h-3 text-amber-400 fill-amber-400" />
                  <span>Featured Sci-Fi Drama</span>
                </div>

                <div className="absolute bottom-4 left-4 right-4 text-white">
                  <div className="text-xs font-mono text-slate-300 uppercase tracking-widest">
                    A Film by Eliza Reed
                  </div>
                  <h4 className="text-2xl font-extrabold text-white tracking-tight">
                    THE HORIZON LINE
                  </h4>
                </div>
              </div>

              {/* Campaign Stats Mock */}
              <div className="grid grid-cols-3 gap-3 text-center p-3 rounded-xl bg-slate-50 border border-slate-100 text-xs">
                <div>
                  <div className="font-mono text-slate-400 text-[10px] uppercase">Target Capital</div>
                  <div className="font-bold text-slate-900 text-sm">$2.4M</div>
                </div>
                <div>
                  <div className="font-mono text-slate-400 text-[10px] uppercase">Community Backers</div>
                  <div className="font-bold text-slate-900 text-sm">1,240+</div>
                </div>
                <div>
                  <div className="font-mono text-slate-400 text-[10px] uppercase">Stage</div>
                  <div className="font-bold text-amber-700 text-sm">Pre-Production</div>
                </div>
              </div>

              {/* Action */}
              <button
                onClick={onOpenWaitlist}
                className="w-full py-3.5 rounded-xl bg-slate-900 text-white font-semibold text-xs uppercase tracking-wider hover:bg-slate-800 transition-colors flex items-center justify-center gap-2"
              >
                <span>Join Waitlist for Priority Access to Projects</span>
                <ArrowRight className="w-4 h-4" />
              </button>
            </div>
          </div>

        </div>

      </div>
    </section>
  );
}
