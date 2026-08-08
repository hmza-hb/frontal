"use client";

import { useState } from "react";
import Navbar from "@/components/Navbar";
import WaitlistModal from "@/components/WaitlistModal";
import HeroSection from "@/components/sections/HeroSection";
import ProblemSection from "@/components/sections/ProblemSection";
import VisionSection from "@/components/sections/VisionSection";
import HowItWorksSection from "@/components/sections/HowItWorksSection";
import WhyBffSection from "@/components/sections/WhyBffSection";
import WhyNowSection from "@/components/sections/WhyNowSection";
import FinalCtaSection from "@/components/sections/FinalCtaSection";
import Footer from "@/components/Footer";

export default function Home() {
  const [waitlistOpen, setWaitlistOpen] = useState(false);
  const [selectedRole, setSelectedRole] = useState<"backer" | "creator" | "investor">("backer");

  const handleOpenWaitlist = (role: "backer" | "creator" | "investor" = "backer") => {
    setSelectedRole(role);
    setWaitlistOpen(true);
  };

  return (
    <main className="min-h-screen bg-slate-50 text-slate-900 flex flex-col relative selection:bg-slate-900 selection:text-white">
      {/* Floating Capsule Header */}
      <Navbar onOpenWaitlist={() => handleOpenWaitlist("backer")} />

      {/* Landing Page 7 Engineered Sections */}
      <HeroSection onOpenWaitlist={() => handleOpenWaitlist("backer")} />
      <ProblemSection />
      <VisionSection />
      <HowItWorksSection onOpenWaitlist={() => handleOpenWaitlist("backer")} />
      <WhyBffSection />
      <WhyNowSection />
      <FinalCtaSection onOpenWaitlist={() => handleOpenWaitlist("backer")} />

      {/* Global Footer */}
      <Footer onOpenWaitlist={() => handleOpenWaitlist("backer")} />

      {/* Interactive Waitlist Dialog Modal */}
      <WaitlistModal
        isOpen={waitlistOpen}
        onClose={() => setWaitlistOpen(false)}
        defaultRole={selectedRole}
      />
    </main>
  );
}